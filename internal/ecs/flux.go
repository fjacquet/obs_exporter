package ecs

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
)

// Flux is the opt-in collector for metric families the management API does
// not serve, sourced from the cluster's Flux/InfluxDB monitoring store. Its
// query table decides which source owns the three contested per-node names
// that Registry's arbitration keeps Nodes from also emitting (ADR-0006).
type Flux struct{}

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
		for _, key := range []string{n.Nodename, shortHost(n.Nodename), n.MgmtIP, n.DataIP} {
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
// docs/metrics.md. The three buckets divide by scope and by whether values
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

// Collect issues one query per measurement and maps the rows onto samples. A
// transport or auth failure fails the whole collector (ecs_collector_up=0); a
// measurement the cluster does not carry answers with no rows and yields a
// warning plus absent samples, because measurement names are undocumented and a
// rename must not take the other seven down with it.
func (Flux) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	mapper, err := newNodeMapper(ctx, c)
	if err != nil {
		return nil, err
	}

	var out []Sample
	var unmapped float64
	for _, q := range fluxQueries {
		var resp fluxResp
		if err := c.Post(ctx, fluxPath, map[string]string{"query": q.script()}, &resp); err != nil {
			return nil, fmt.Errorf("flux %s/%s: %w", q.bucket, q.measurement, err)
		}
		rows := resp.rows()
		if len(rows) == 0 {
			log.WithFields(log.Fields{"cluster": c.Name(), "bucket": q.bucket, "measurement": q.measurement}).
				Warn("Flux measurement returned no rows; its samples are absent this cycle")
			continue
		}
		samples, miss := q.samples(rows, mapper)
		out = append(out, samples...)
		unmapped += miss
	}

	// Always emitted, including as 0: "mapping worked" is the information that
	// distinguishes a healthy cycle from one where every node tag joined nothing.
	out = append(out, Sample{
		Name:   "ecs_collector_unmapped_nodes",
		Labels: []Label{{"collector", "flux"}},
		Value:  unmapped,
	})
	return out, nil
}

// samples maps one measurement's rows, returning the samples and how many rows
// were dropped for an unresolvable host.
func (q fluxQuery) samples(rows []fluxRow, mapper *nodeMapper) ([]Sample, float64) {
	var out []Sample
	var unmapped float64
	for _, row := range rows {
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
			host, ok := row.value("host")
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
	return out, unmapped
}
