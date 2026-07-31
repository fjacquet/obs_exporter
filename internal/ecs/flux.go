package ecs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
)

// Flux is the opt-in collector for metric families the management API does
// not serve, sourced from the cluster's Flux/InfluxDB monitoring store. Its
// query table decides which source owns the four contested per-node names
// that Registry's arbitration keeps Nodes from also emitting (ADR-0006).
type Flux struct {
	// DTOwnedByDT suppresses the per-node DT query when the opt-in DT collector
	// runs: it serves unready and unknown per node as well, so where it is
	// reachable it is the richer source and keeps the name (ADR-0006).
	DTOwnedByDT bool
	// now overrides the clock. Zero means time.Now; tests set it so fixtures
	// captured at a fixed instant are not all judged stale.
	now func() time.Time
}

func (f Flux) clock() time.Time {
	if f.now == nil {
		return time.Now()
	}
	return f.now()
}

// Name identifies this collector in ecs_collector_up.
func (Flux) Name() string { return "flux" }

// nodeMapper resolves a Flux `host` tag onto the /vdc/nodes nodename every other
// collector uses as the node label. Flux reports host as an FQDN in the 4.3
// reference's example and node_id as a UUID, and neither is guaranteed to equal
// nodename, so each inventory node is indexed under every identifier it
// publishes. A series that does not join is worse than an absent one.
type nodeMapper struct {
	byKey map[string]string
}

// newNodeMapper reads the node inventory and indexes it.
func newNodeMapper(ctx context.Context, c ecsclient.Client) (*nodeMapper, error) {
	var inv vdcNodesResp
	if err := c.Get(ctx, pathVdcNodes, &inv); err != nil {
		return nil, err
	}
	m := &nodeMapper{byKey: map[string]string{}}
	for _, n := range inv.Node {
		// The same precedence the DT collector uses, so both label a node identically.
		label := cmp.Or(n.Nodename, n.MgmtIP, n.DataIP)
		if label == "" {
			continue
		}
		for _, key := range []string{n.Nodename, shortHost(n.Nodename), n.MgmtIP, n.DataIP, n.Data2IP, n.PrivateIP} {
			if key == "" {
				continue
			}
			k := strings.ToLower(key)
			// A key two different nodes both claim cannot identify either of them.
			// Blanking it makes lookup fail, so the row is dropped and counted,
			// rather than silently attributed to whichever node was indexed last.
			if prior, seen := m.byKey[k]; seen && prior != label {
				m.byKey[k] = ""
				continue
			}
			m.byKey[k] = label
		}
	}
	return m, nil
}

// lookup resolves a Flux host tag, trying it whole and then truncated at the
// first dot, so a qualified name joins a bare nodename.
func (m *nodeMapper) lookup(host string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if n, ok := m.byKey[h]; ok && n != "" {
		return n, true
	}
	if n, ok := m.byKey[shortHost(h)]; ok && n != "" {
		return n, true
	}
	return "", false
}

// shortHost truncates an FQDN at its first dot. An IP address is returned
// unchanged: truncating one yields a meaningless key that could collide across
// nodes sharing a prefix.
func shortHost(h string) string {
	if net.ParseIP(h) != nil {
		return h
	}
	if i := strings.Index(h, "."); i >= 0 {
		return h[:i]
	}
	return h
}

// fluxPath is the Flux query endpoint, served on the same management port and
// accepting the same X-SDS-AUTH-TOKEN as every other call this exporter makes.
const fluxPath = "/flux/api/external/v2/query"

// fluxRange is how far back each query looks. The store punishes wide windows —
// the 4.3 release notes record Grafana timing out against it at one hour — while
// statDataHead writes points five minutes apart, so a five-minute window can
// legitimately return nothing. Fifteen minutes keeps two to three points of
// margin an order of magnitude below the failing window.
const fluxRange = "-15m"

// fluxField maps one measurement field onto a metric name, its type, and the
// constant labels that distinguish it from its siblings under the same name.
type fluxField struct {
	field  string
	name   string
	typ    SampleType
	labels []Label
}

// fluxQuery is one measurement's request and its field mapping. One request per
// measurement, closed with |> last() and carrying no host filter, returns the
// newest point per node in a single cluster-wide pass (ADR-0002).
type fluxQuery struct {
	bucket      string
	measurement string
	// extra is an additional filter line, empty for most measurements.
	extra string
	// perNode marks rows that carry a host tag needing a node label.
	perNode bool
	// tagLabels are row tags copied through as labels. A row missing one is
	// dropped rather than emitted with a short label set, because one metric name
	// carries one ordered label-key set (ADR-0006).
	tagLabels []string
	fields    []fluxField
	// maxAge overrides fluxMaxAge for a measurement outside the five-minute
	// cadence class. Zero means the default.
	maxAge time.Duration
	// hostTag is the column carrying the node's identity. Empty means "host".
	// dtquery_dt_dist_host_dt_node_id identifies the node under dt_node_id
	// instead, holding its data_ip rather than a hostname.
	hostTag string
	// dtPerNode marks the query the DT collector owns when it is enabled.
	dtPerNode bool
	// buckets, when set, reads field names as bucket bounds instead of matching
	// them against fields.
	buckets *fluxBuckets
}

// fluxBuckets describes a measurement whose field *names* are histogram bucket
// bounds and whose values are cumulative counts.
//
// statDataHead_performance_internal_latency is the only such measurement, and
// it is the source the ECS dashboard reads its own read/write latency from —
// which is what justifies mapping its id tag onto the op dimension the
// dashboard-sourced family already uses. The store serves no _sum, so
// prometheus.MustNewConstHistogram is unusable and the buckets are published as
// ordinary counters carrying an le label, which is what histogram_quantile
// consumes.
type fluxBuckets struct {
	// name is the family name; _bucket and _count are appended to it.
	name string
	// idLabels maps the id tag onto its op label value. A row whose id is absent
	// from this map is dropped rather than published under a short label set.
	idLabels map[string]string
}

// script renders the Flux program for this measurement.
func (q fluxQuery) script() string {
	s := fmt.Sprintf("from(bucket:%q)\n  |> range(start: %s)\n  |> filter(fn: (r) => r._measurement == %q)\n",
		q.bucket, fluxRange, q.measurement)
	if q.extra != "" {
		s += q.extra + "\n"
	}
	return s + "  |> last()"
}

// fluxQueries is the measurement-to-metric mapping, documented in
// docs/metrics/flux.md. The three buckets divide by scope and by whether values
// arrive pre-rated: monitoring_op is per-node system state, monitoring_main
// per-node cumulative counters, monitoring_vdc VDC-wide values already expressed
// as rates — which is why the last group are gauges and must never be rate()d.
var fluxQueries = []fluxQuery{
	{
		bucket: "monitoring_op", measurement: "cpu",
		// The cpu measurement is tagged per core; cpu-total keeps one series per
		// node, matching the shape nodes.go reserves for this name.
		extra:   `  |> filter(fn: (r) => r.cpu == "cpu-total")`,
		perNode: true,
		fields: []fluxField{
			{field: "usage_user", name: "ecs_node_cpu_utilization_percent"},
		},
	},
	{
		bucket: "monitoring_op", measurement: "mem", perNode: true,
		fields: []fluxField{
			{field: "used_percent", name: "ecs_node_memory_utilization_percent"},
			{field: "used", name: "ecs_node_memory_used_bytes"},
		},
	},
	{
		bucket: "monitoring_op", measurement: "net", perNode: true,
		// net has no total row to fall back on, so the interface dimension is kept
		// rather than summed — adding a management and a data NIC together would
		// produce a number nobody asked for.
		tagLabels: []string{"interface"},
		fields: []fluxField{
			{field: "bytes_recv", name: "ecs_node_network_bytes_total", typ: Counter, labels: []Label{{"direction", "received"}}},
			{field: "bytes_sent", name: "ecs_node_network_bytes_total", typ: Counter, labels: []Label{{"direction", "transmitted"}}},
		},
	},
	{
		// Tagged {process, tag} only: cluster-wide, never per node.
		bucket: "monitoring_op", measurement: "dtquery_dt_status",
		fields: []fluxField{
			{field: "total", name: "ecs_cluster_dt_total"},
			{field: "unready", name: "ecs_cluster_dt_unready"},
			{field: "unknown", name: "ecs_cluster_dt_unknown"},
		},
	},
	{
		// Tagged {dt_node_id, process, tag}: no host column, but dt_node_id
		// carries the node's data_ip, which the inventory indexes. On the live
		// 4.3 capture the per-node counts sum to dtquery_dt_status's cluster
		// total, so this is that total's breakdown under another column name.
		// Owned by the DT collector when that one runs — see Registry.
		bucket: "monitoring_op", measurement: "dtquery_dt_dist_host_dt_node_id",
		perNode: true, hostTag: "dt_node_id", dtPerNode: true,
		fields: []fluxField{
			{field: "count_i", name: "ecs_node_dt_total"},
		},
	},
	{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_transactions", perNode: true,
		fields: []fluxField{
			{field: "succeed_request_counter", name: "ecs_node_requests_total", typ: Counter, labels: []Label{{"outcome", "success"}}},
			{field: "failed_request_counter", name: "ecs_node_requests_total", typ: Counter, labels: []Label{{"outcome", "failed"}}},
		},
	},
	{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_throughput", perNode: true,
		fields: []fluxField{
			{field: "total_read_requests_size", name: "ecs_node_request_bytes_total", typ: Counter, labels: []Label{{"op", "read"}}},
			{field: "total_write_requests_size", name: "ecs_node_request_bytes_total", typ: Counter, labels: []Label{{"op", "write"}}},
		},
	},
	{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_latency",
		perNode: true,
		buckets: &fluxBuckets{
			name:     "ecs_node_transaction_latency_milliseconds",
			idLabels: map[string]string{"ttfb_read": "read", "ttlb_write": "write"},
		},
	},
	{
		bucket: "monitoring_vdc", measurement: "cq_performance_transaction",
		fields: []fluxField{
			{field: "succeed_request_counter", name: "ecs_cluster_requests_per_second", labels: []Label{{"outcome", "success"}}},
			{field: "failed_request_counter", name: "ecs_cluster_requests_per_second", labels: []Label{{"outcome", "failed"}}},
		},
	},
	{
		bucket: "monitoring_vdc", measurement: "cq_performance_throughput",
		fields: []fluxField{
			{field: "total_read_requests_size", name: "ecs_cluster_request_bytes_per_second", labels: []Label{{"op", "read"}}},
			{field: "total_write_requests_size", name: "ecs_cluster_request_bytes_per_second", labels: []Label{{"op", "write"}}},
		},
	},
}

// fluxFatal reports whether an error condemns the whole collector rather than
// one measurement.
//
// Anything that is not a per-request API error — a transport failure, a login
// failure, a cancelled context — is global by construction: it says nothing
// about the query and everything about the connection. An API error is global
// only when ECS itself says retrying can never help, which is how a permission
// refusal (HTTP 500, code 6401) is told from a query bug (HTTP 500, a compile
// error) that leaves the other measurements perfectly collectable.
func fluxFatal(err error) bool {
	var api *ecsclient.APIError
	if !errors.As(err, &api) {
		return true
	}
	return api.Permanent()
}

// Collect issues one query per measurement and maps the rows onto samples. A
// transport failure, a login failure, or a permanent API refusal (a permission
// error ECS says retrying can never fix) fails the whole collector
// (ecs_collector_up=0) without issuing the remaining queries. A query-scoped
// failure — a renamed measurement returning no rows, or a malformed query
// ECS rejects with a compile error — is logged and skipped, because
// measurement names are undocumented and a rename must not take the other
// seven down with it. Collect still fails if every query failed: tolerating
// one bad query must not disguise a collector that produced nothing at all.
func (f Flux) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	mapper, err := newNodeMapper(ctx, c)
	if err != nil {
		return nil, err
	}

	now := f.clock()
	var out []Sample
	var unmapped float64
	var attempted, succeeded int
	for _, q := range fluxQueries {
		if q.dtPerNode && f.DTOwnedByDT {
			continue
		}
		attempted++
		var resp fluxResp
		if err := c.Post(ctx, fluxPath, map[string]string{"query": q.script()}, &resp); err != nil {
			wrapped := fmt.Errorf("flux %s/%s: %w", q.bucket, q.measurement, err)
			if fluxFatal(err) {
				return nil, wrapped
			}
			log.WithFields(log.Fields{
				"cluster": c.Name(), "bucket": q.bucket, "measurement": q.measurement, "err": err,
			}).Warn("Flux query failed; its samples are absent this cycle")
			continue
		}
		succeeded++
		rows := resp.rows()
		if len(rows) == 0 {
			log.WithFields(log.Fields{"cluster": c.Name(), "bucket": q.bucket, "measurement": q.measurement}).
				Warn("Flux measurement returned no rows; its samples are absent this cycle")
			continue
		}
		samples, miss, stale := q.samples(rows, mapper, now)
		out = append(out, samples...)
		unmapped += miss
		_ = stale // reported by the per-measurement debug line in Task 9
	}
	if attempted > 0 && succeeded == 0 {
		return nil, fmt.Errorf("flux: all %d queries failed", attempted)
	}

	// Always emitted, including as 0: "mapping worked" is the information that
	// distinguishes a healthy cycle from one where every node tag joined nothing.
	// Because it is unconditional, collectCluster (collector.go) excludes it from
	// domainSamples by name — otherwise this one housekeeping sample alone could
	// keep ecs_up at 1 on a cycle where every measurement renamed out from under
	// the query table and nothing else in the cluster produced real data.
	out = append(out, Sample{
		Name:   unmappedNodesMetric,
		Labels: []Label{{"collector", "flux"}},
		Value:  unmapped,
	})
	return out, nil
}

// unmappedNodesMetric is Flux's housekeeping sample name (see Collect above).
// collectCluster keys off this constant, not a literal, so the two stay in sync.
const unmappedNodesMetric = "ecs_collector_unmapped_nodes"

// samples maps one measurement's rows, returning the samples, how many rows were
// dropped for an unresolvable host, and how many were dropped as stale.
func (q fluxQuery) samples(rows []fluxRow, mapper *nodeMapper, now time.Time) ([]Sample, float64, float64) {
	var out []Sample
	var unmapped, stale float64
	limit := q.maxAgeOrDefault()
	for _, row := range rows {
		age, dated := row.age(now)
		if !dated || age > limit {
			stale++
			continue
		}
		field, ok := row.value("_field")
		if !ok {
			continue
		}
		v, ok := row.num("_value")
		if !ok {
			continue // absent, never zero
		}

		var base []Label
		if q.perNode {
			tag := q.hostTag
			if tag == "" {
				tag = "host"
			}
			host, ok := row.value(tag)
			if !ok {
				continue
			}
			node, ok := mapper.lookup(host)
			if !ok {
				unmapped++
				continue
			}
			base = append(base, Label{"node", node})
		}
		short := false
		for _, tag := range q.tagLabels {
			tv, ok := row.value(tag)
			if !ok {
				short = true // a partial label set would break the name's schema
				break
			}
			base = append(base, Label{tag, tv})
		}
		if short {
			continue
		}

		if q.buckets != nil {
			op, ok := row.value("id")
			if !ok {
				continue
			}
			opLabel, ok := q.buckets.idLabels[op]
			if !ok {
				continue // an id the mapping does not cover
			}
			labels := append(slices.Clone(base), Label{"op", opLabel})
			out = append(out, Sample{
				Name:   q.buckets.name + "_bucket",
				Labels: append(slices.Clone(labels), Label{"le", field}),
				Value:  v,
				Type:   Counter,
			})
			// _count is the +Inf bucket: every observation falls under it.
			if field == "+Inf" {
				out = append(out, Sample{
					Name:   q.buckets.name + "_count",
					Labels: labels,
					Value:  v,
					Type:   Counter,
				})
			}
			continue
		}

		for _, f := range q.fields {
			if f.field != field {
				continue
			}
			out = append(out, Sample{
				Name:   f.name,
				Labels: append(slices.Clone(base), f.labels...),
				Value:  v,
				Type:   f.typ,
			})
		}
	}
	return out, unmapped, stale
}
