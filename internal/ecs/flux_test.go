package ecs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
)

// captureInstant is a moment shortly after every flux_*.json fixture was
// written on the live cluster, so fixture rows read as fresh (see fluxMaxAge).
var captureInstant = time.Date(2026, 7, 31, 8, 38, 30, 0, time.UTC)

func TestNodeMapperResolvesFluxHosts(t *testing.T) {
	// The vdc-nodes fixture names supr01-r01 at 10.0.0.1 / 10.1.0.1. Flux reports
	// host as an FQDN in the reference's example, so the mapper must join a
	// qualified name onto the bare nodename every other collector labels with.
	m, err := newNodeMapper(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ host, want string }{
		{"supr01-r01", "supr01-r01"},
		{"supr01-r01.example.com", "supr01-r01"},
		{"SUPR01-R01.EXAMPLE.COM", "supr01-r01"},
		{"10.0.0.1", "supr01-r01"},
		{"10.1.0.1", "supr01-r01"},
	} {
		got, ok := m.lookup(tc.host)
		if !ok || got != tc.want {
			t.Errorf("lookup(%q) = %q,%v; want %q,true", tc.host, got, ok, tc.want)
		}
	}
}

func TestNodeMapperRejectsUnknownHosts(t *testing.T) {
	// A host that joins nothing must fail loudly to its caller rather than
	// produce a series no dashboard query can line up with the rest.
	m, err := newNodeMapper(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.lookup("someone-elses-node.example.com"); ok {
		t.Errorf("lookup of an unknown host returned %q, want no match", got)
	}
}

func TestShortHostLeavesIPsAlone(t *testing.T) {
	// Truncating an IPv4 address at its first dot produces a meaningless key
	// that could collide across nodes.
	if got := shortHost("10.0.0.1"); got != "10.0.0.1" {
		t.Errorf("shortHost(IP) = %q, want it unchanged", got)
	}
	if got := shortHost("n1.example.com"); got != "n1" {
		t.Errorf("shortHost(FQDN) = %q, want n1", got)
	}
}

func TestNodeMapperRejectsCollidingShortHost(t *testing.T) {
	// Two different nodes whose short hostnames collide: a wrong join is worse
	// than no join, so the ambiguous key must resolve to neither node.
	c := mockClient(t)
	c.Responses[pathVdcNodes] = `{"node":[
		{"nodename":"n1.dc1.example.com","mgmt_ip":"10.0.0.1","data_ip":"10.1.0.1"},
		{"nodename":"n1.dc2.example.com","mgmt_ip":"10.0.0.2","data_ip":"10.1.0.2"}
	]}`
	m, err := newNodeMapper(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.lookup("n1"); ok {
		t.Errorf("lookup(%q) = %q,true; want no match on the colliding short host", "n1", got)
	}
	if got, ok := m.lookup("n1.dc1.example.com"); !ok || got != "n1.dc1.example.com" {
		t.Errorf("lookup of full FQDN = %q,%v; want %q,true", got, ok, "n1.dc1.example.com")
	}
	if got, ok := m.lookup("n1.dc2.example.com"); !ok || got != "n1.dc2.example.com" {
		t.Errorf("lookup of full FQDN = %q,%v; want %q,true", got, ok, "n1.dc2.example.com")
	}
}

func TestNodeMapperResolvesFlatNetworkNode(t *testing.T) {
	// A flat-network node whose mgmt_ip equals its data_ip, and whose
	// unqualified nodename equals its own shortHost, re-registers the same key
	// under itself repeatedly. That must not be mistaken for a collision
	// between two different nodes.
	c := mockClient(t)
	c.Responses[pathVdcNodes] = `{"node":[
		{"nodename":"supr01-r01","mgmt_ip":"10.0.0.1","data_ip":"10.0.0.1"}
	]}`
	m, err := newNodeMapper(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"supr01-r01", "10.0.0.1"} {
		if got, ok := m.lookup(host); !ok || got != "supr01-r01" {
			t.Errorf("lookup(%q) = %q,%v; want %q,true", host, got, ok, "supr01-r01")
		}
	}
}

// fluxMock answers every Flux POST with the fixture chosen by the measurement
// named in the query body. The Mock keys responses by path alone, and all eight
// queries share one path, so the collector's request bodies drive the routing.
func fluxMock(t *testing.T, byMeasurement map[string]string) ecsclient.Client {
	t.Helper()
	return &fluxClient{Client: mockClient(t), bodies: byMeasurement, t: t}
}

type fluxClient struct {
	ecsclient.Client
	bodies map[string]string
	t      *testing.T
	// queries records every query body actually POSTed, in order, so a test
	// can assert on request *count* — the short-circuit is a promise that
	// Collect stops issuing queries, not merely that it returns an error, and
	// only a recording of what was actually sent can tell those apart. A later
	// task on this branch needs the same field to assert a query was *not*
	// issued, hence recording the raw string rather than just a count.
	queries []string
	postErr error
}

func (f *fluxClient) Post(_ context.Context, path string, body, out any) error {
	if q, ok := body.(map[string]string); ok {
		f.queries = append(f.queries, q["query"])
	}
	if f.postErr != nil {
		return f.postErr
	}
	if path != fluxPath {
		return fmt.Errorf("unexpected POST to %s", path)
	}
	q, ok := body.(map[string]string)
	if !ok {
		f.t.Fatalf("query body is %T, want map[string]string", body)
	}
	for measurement, fixtureName := range f.bodies {
		if strings.Contains(q["query"], `"`+measurement+`"`) {
			return json.Unmarshal([]byte(fixture(f.t, fixtureName)), out)
		}
	}
	// A measurement the cluster does not carry answers 200 with no rows.
	return json.Unmarshal([]byte(`{"Series":[]}`), out)
}

func collectFlux(t *testing.T, byMeasurement map[string]string) []Sample {
	t.Helper()
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, byMeasurement))
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestFluxCollectPerNodeGauges(t *testing.T) {
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 5.119769342229881, Label{"node", "supr01-r01"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 4.370401774443806, Label{"node", "supr01-r02"})
	s, _ := findSample(samples, "ecs_node_cpu_utilization_percent", Label{"node", "supr01-r01"})
	if s.Type != Gauge {
		t.Error("cpu utilization must be a gauge")
	}
}

func TestFluxCollectNetworkCounters(t *testing.T) {
	samples := collectFlux(t, map[string]string{"net": "flux_net.json"})
	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_network_bytes_total", 34804069544351, n1, Label{"interface", "public"}, Label{"direction", "received"})
	mustSample(t, samples, "ecs_node_network_bytes_total", 49132581189845, n1, Label{"interface", "public"}, Label{"direction", "transmitted"})

	s, _ := findSample(samples, "ecs_node_network_bytes_total", n1, Label{"direction", "received"})
	if s.Type != Counter {
		t.Error("network bytes must be a counter: the guide documents these fields as resetting on datahead restart")
	}
	// Label order is part of the metric's schema (ADR-0006).
	wantKeys := []string{"node", "interface", "direction"}
	gotKeys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		gotKeys[i] = l.Key
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("label keys = %v, want %v", gotKeys, wantKeys)
	}
}

func TestFluxCollectClusterScopedDT(t *testing.T) {
	// dtquery_dt_status is tagged {process, tag} only — it is cluster-wide, and
	// must not pretend to be per-node.
	samples := collectFlux(t, map[string]string{"dtquery_dt_status": "flux_dt_status.json"})
	mustSample(t, samples, "ecs_cluster_dt_total", 1936)
	mustSample(t, samples, "ecs_cluster_dt_unready", 0)
	mustSample(t, samples, "ecs_cluster_dt_unknown", 0)
	s, _ := findSample(samples, "ecs_cluster_dt_total")
	if len(s.Labels) != 0 {
		t.Errorf("cluster DT carries labels %v, want none", s.Labels)
	}
}

// TestFluxClusterDTFieldMappingIsDistinct covers the same measurement as
// TestFluxCollectClusterScopedDT above, from a different angle. The real
// capture behind flux_dt_status.json happens to be a healthy cluster: unready
// and unknown are both genuinely 0, which is exactly what that test needs to
// prove a real zero survives as a present sample rather than being dropped
// (ADR-0007, absent-never-zero). But two zeros can't also prove the fields
// land on the right names — a bug that swapped the unready/unknown mapping,
// or mixed total into either, would pass that test silently. This test
// hand-builds three distinct field values instead of reading a fixture, so
// such a mix-up fails here even though the live data can't exercise it.
func TestFluxClusterDTFieldMappingIsDistinct(t *testing.T) {
	var dtStatus fluxQuery
	for _, q := range fluxQueries {
		if q.measurement == "dtquery_dt_status" {
			dtStatus = q
		}
	}
	// _time is set to a moment the fixed clock below reads as fresh: an undated
	// row would be dropped as stale before its field mapping is ever checked.
	rows := []fluxRow{
		{cols: map[string]string{"_field": "total", "_value": "10", "_time": "2026-07-31T08:36:00Z"}},
		{cols: map[string]string{"_field": "unready", "_value": "20", "_time": "2026-07-31T08:36:00Z"}},
		{cols: map[string]string{"_field": "unknown", "_value": "30", "_time": "2026-07-31T08:36:00Z"}},
	}
	// Cluster-wide (perNode is false for this measurement), so samples never
	// dereferences the mapper: nil stands in for "no node join needed."
	samples, unmapped, stale := dtStatus.samples(rows, nil, captureInstant)
	if unmapped != 0 {
		t.Fatalf("unmapped = %v, want 0: dtquery_dt_status carries no host to map", unmapped)
	}
	if stale != 0 {
		t.Fatalf("stale = %v, want 0: every row is dated fresh relative to captureInstant", stale)
	}
	mustSample(t, samples, "ecs_cluster_dt_total", 10)
	mustSample(t, samples, "ecs_cluster_dt_unready", 20)
	mustSample(t, samples, "ecs_cluster_dt_unknown", 30)
}

func TestFluxQueryScriptShape(t *testing.T) {
	var cpu fluxQuery
	for _, q := range fluxQueries {
		if q.measurement == "cpu" {
			cpu = q
		}
	}
	script := cpu.script()
	for _, want := range []string{
		`from(bucket:"monitoring_op")`,
		`range(start: -15m)`,
		`r._measurement == "cpu"`,
		`r.cpu == "cpu-total"`,
		`|> last()`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("cpu script missing %q:\n%s", want, script)
		}
	}
	// A host filter would turn one cluster-wide request into N+1 per node.
	if strings.Contains(script, "r.host ==") {
		t.Errorf("script filters by host:\n%s", script)
	}
}

func TestFluxCollectFailsOnEndpointError(t *testing.T) {
	// An unreachable or unauthorized endpoint degrades this collector alone:
	// returning an error is what drives ecs_collector_up{collector="flux"}=0.
	c := mockClient(t)
	f := Flux{now: func() time.Time { return captureInstant }}
	if _, err := f.Collect(t.Context(), &fluxClient{Client: c, bodies: nil, t: t, postErr: errors.New("401 Unauthorized")}); err == nil {
		t.Error("Collect must return an error when the Flux endpoint rejects the query")
	}
}

// erroringFluxClient fails the named measurements and serves fixtures for the
// rest, so a partial failure can be told from a total one.
type erroringFluxClient struct {
	*fluxClient
	errByMeasurement map[string]error
}

func (e *erroringFluxClient) Post(ctx context.Context, path string, body, out any) error {
	q, _ := body.(map[string]string)
	for measurement, err := range e.errByMeasurement {
		if strings.Contains(q["query"], `"`+measurement+`"`) {
			// An intercepted call never reaches fluxClient.Post, so record it
			// here — otherwise a query this type fails would vanish from the
			// count fluxClient.Post is meant to keep.
			e.queries = append(e.queries, q["query"])
			return err
		}
	}
	return e.fluxClient.Post(ctx, path, body, out)
}

// permissionRefusal is the decoded, permanent shape a real ECS 500 takes when
// the authenticated account lacks the roles for this endpoint: retrying it,
// on this or any other measurement, can never succeed.
func permissionRefusal() error {
	return &ecsclient.APIError{
		Method: "POST", Path: fluxPath, Status: 500,
		Code: ecsclient.CodeInsufficientPermissions, Description: "Insufficient permissions", Decoded: true,
	}
}

func TestFluxPermissionRefusalFailsTheWholeCollector(t *testing.T) {
	// Nothing this collector asks for will work; failing fast is both correct
	// and the difference between one request per cycle and ten. cpu is the
	// first entry in fluxQueries, so a genuine short-circuit issues exactly
	// that one query and stops -- never reaching mem, net, or the rest.
	c := &erroringFluxClient{
		fluxClient:       &fluxClient{Client: mockClient(t), bodies: map[string]string{"cpu": "flux_cpu.json"}, t: t},
		errByMeasurement: map[string]error{"cpu": permissionRefusal()},
	}
	f := Flux{now: func() time.Time { return captureInstant }}
	if _, err := f.Collect(t.Context(), c); err == nil {
		t.Fatal("a permission refusal must fail the collector")
	}
	// The behavioral property, not just the return value: a client that keeps
	// looping past the fatal query and only fails via err != nil would still
	// pass the check above while quietly issuing all len(fluxQueries) requests.
	if got := len(c.queries); got != 1 {
		t.Fatalf("issued %d queries after a permission refusal, want exactly 1 (the short-circuit): %v", got, c.queries)
	}
}

func TestFluxOneBadQueryLeavesTheOthersStanding(t *testing.T) {
	// A compile error is scoped to one query. Taking the other nine down with it
	// costs the operator every metric this collector exists to provide.
	c := &erroringFluxClient{
		fluxClient: &fluxClient{Client: mockClient(t), bodies: map[string]string{
			"cpu": "flux_cpu.json",
			"mem": "flux_mem.json",
		}, t: t},
		errByMeasurement: map[string]error{
			"mem": &ecsclient.APIError{Method: "POST", Path: fluxPath, Status: 500,
				Body: `{"error":"failed to compile query: undefined identifier"}`},
		},
	}
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), c)
	if err != nil {
		t.Fatalf("one failing query must not fail the collector: %v", err)
	}
	if _, ok := findSample(samples, "ecs_node_cpu_utilization_percent"); !ok {
		t.Error("cpu samples were lost to an unrelated query's failure")
	}
	if _, ok := findSample(samples, "ecs_node_memory_utilization_percent"); ok {
		t.Error("the failed measurement emitted samples")
	}
}

func TestFluxTransportErrorFailsTheWholeCollector(t *testing.T) {
	// A plain transport error is not an *ecsclient.APIError, so fluxFatal must
	// treat it as fatal and Collect must return on the very first query — this
	// exercises the "not an APIError" arm of fluxFatal, not the succeeded==0
	// tally below (see TestFluxAllQueriesFailNonFatallyFailsTheCollector for that).
	c := &erroringFluxClient{
		fluxClient:       &fluxClient{Client: mockClient(t), bodies: nil, t: t},
		errByMeasurement: nil,
	}
	c.postErr = errors.New("500 Internal Server Error")
	f := Flux{now: func() time.Time { return captureInstant }}
	if _, err := f.Collect(t.Context(), c); err == nil {
		t.Fatal("Collect must fail when the transport itself is broken")
	}
}

// TestFluxAllQueriesFailNonFatallyFailsTheCollector reaches the succeeded==0
// branch that TestFluxTransportErrorFailsTheWholeCollector cannot: every query
// fails, but each with a query-scoped, non-fatal *ecsclient.APIError (an
// undecoded body, the same shape a Flux compile error takes), so fluxFatal
// returns false for every one of them and Collect only sees the tally. Without
// this test, "tolerate a per-query failure" and "still fail when nothing
// succeeded" could silently regress into "always succeed."
func TestFluxAllQueriesFailNonFatallyFailsTheCollector(t *testing.T) {
	compileError := &ecsclient.APIError{Method: "POST", Path: fluxPath, Status: 500,
		Body: `{"error":"failed to compile query: undefined identifier"}`}
	errs := make(map[string]error, len(fluxQueries))
	for _, q := range fluxQueries {
		errs[q.measurement] = compileError
	}
	c := &erroringFluxClient{
		fluxClient:       &fluxClient{Client: mockClient(t), bodies: nil, t: t},
		errByMeasurement: errs,
	}
	f := Flux{now: func() time.Time { return captureInstant }}
	if _, err := f.Collect(t.Context(), c); err == nil {
		t.Fatal("Collect must fail when every query failed, even non-fatally")
	}
}

// TestFluxFatalClassifiesErrors covers fluxFatal directly. It exists because
// TestFluxTransportErrorFailsTheWholeCollector cannot, by itself, prove that a
// plain transport error is classified fatal: every query in that test shares
// the same postErr, so even a fluxFatal that always returned false would still
// fail Collect via the succeeded==0 tally. Only a direct call isolates the
// classification rule from that fallback.
func TestFluxFatalClassifiesErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"transport failure, not an APIError", errors.New("connection refused"), true},
		{"login failure, not an APIError", errors.New("401 Unauthorized"), true},
		{"decoded permission refusal (retryable false)", &ecsclient.APIError{
			Status: 500, Code: ecsclient.CodeInsufficientPermissions, Description: "Insufficient permissions",
			Decoded: true, Retryable: false,
		}, true},
		{"decoded, retryable true, not the permission code", &ecsclient.APIError{
			Status: 500, Code: 1234, Decoded: true, Retryable: true,
		}, false},
		{"undecoded body (a Flux compile error)", &ecsclient.APIError{
			Status: 500, Body: `{"error":"failed to compile query: undefined identifier"}`,
		}, false},
	} {
		if got := fluxFatal(tc.err); got != tc.want {
			t.Errorf("%s: fluxFatal(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestFluxCollectSurvivesRenamedMeasurement(t *testing.T) {
	// Measurement names are undocumented and unversioned: net/utilization is
	// listed in 3.8 and gone in 4.3. One missing measurement must not take the
	// other seven with it.
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 5.119769342229881, Label{"node", "supr01-r01"})
	for _, absent := range []string{"ecs_cluster_dt_total", "ecs_node_requests_total"} {
		if _, ok := findSample(samples, absent); ok {
			t.Errorf("%s emitted from an empty measurement", absent)
		}
	}
}

func TestFluxCountsUnmappedHosts(t *testing.T) {
	// flux_net.json carries one row for a host absent from the inventory. Without
	// this counter, a cluster whose tag space we guessed wrong reports a healthy
	// collector producing no data.
	samples := collectFlux(t, map[string]string{"net": "flux_net.json"})
	mustSample(t, samples, "ecs_collector_unmapped_nodes", 1, Label{"collector", "flux"})
	if _, ok := findSample(samples, "ecs_node_network_bytes_total", Label{"node", "not-in-this-cluster.example.com"}); ok {
		t.Error("an unmappable host produced a series that cannot join the others")
	}
}

func TestFluxCollectEmitsZeroUnmappedOnSuccess(t *testing.T) {
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_collector_unmapped_nodes", 0, Label{"collector", "flux"})
}

func TestFluxCollectsPerNodeDT(t *testing.T) {
	// dtquery_dt_dist_host_dt_node_id has no host tag, which is why ADR-0011
	// concluded Flux could not report DT per node. It identifies the node under
	// dt_node_id instead, holding the data_ip the inventory already indexes.
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"dtquery_dt_dist_host_dt_node_id": "flux_dt_dist.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := findSample(samples, "ecs_node_dt_total", Label{"node", "supr01-r01"})
	if !ok {
		t.Fatal("no per-node DT sample for supr01-r01")
	}
	if s.Value <= 0 {
		t.Errorf("ecs_node_dt_total = %v, want the capture's count", s.Value)
	}
	if _, ok := findSample(samples, "ecs_node_dt_total", Label{"node", "supr01-r02"}); !ok {
		t.Error("no per-node DT sample for supr01-r02")
	}
}

func TestFluxSkipsPerNodeDTWhenDTCollectorOwnsIt(t *testing.T) {
	// collectDT serves unready and unknown per node as well, so where it is
	// reachable it keeps the name and Flux must not issue the query at all.
	c := mockClient(t)
	fc := &fluxClient{Client: c, bodies: map[string]string{
		"dtquery_dt_dist_host_dt_node_id": "flux_dt_dist.json",
	}, t: t}
	f := Flux{DTOwnedByDT: true, now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_node_dt_total"); ok {
		t.Error("Flux emitted ecs_node_dt_total while collectDT owns it")
	}
	for _, q := range fc.queries {
		if strings.Contains(q, "dtquery_dt_dist_host_dt_node_id") {
			t.Error("Flux issued the per-node DT query it does not own")
		}
	}
}

func TestFluxClusterDTIsUnaffectedByArbitration(t *testing.T) {
	// The cluster totals have no per-node equivalent and no other owner.
	f := Flux{DTOwnedByDT: true, now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"dtquery_dt_status": "flux_dt_status.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_cluster_dt_total"); !ok {
		t.Error("cluster DT totals must survive per-node arbitration")
	}
}

func TestFluxLatencyBuckets(t *testing.T) {
	// The field names are the bucket bounds and the values are cumulative, with
	// +Inf equal to the last finite bound -- a Prometheus histogram in every
	// respect except that the store serves no _sum.
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"statDataHead_performance_internal_latency": "flux_latency.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	node := Label{"node", "supr01-r01"}
	read := Label{"op", "read"}

	inf, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_bucket", node, read, Label{"le", "+Inf"})
	if !ok {
		t.Fatal("no +Inf bucket")
	}
	if inf.Type != Counter {
		t.Error("histogram buckets are cumulative counters")
	}
	count, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_count", node, read)
	if !ok {
		t.Fatal("no _count series")
	}
	if count.Value != inf.Value {
		t.Errorf("_count = %v, +Inf bucket = %v; they are the same number", count.Value, inf.Value)
	}
	// ttlb_write maps onto the write op, the same dimension the dashboard path
	// uses for this family.
	if _, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_bucket",
		node, Label{"op", "write"}, Label{"le", "+Inf"}); !ok {
		t.Error("no write-op buckets: ttlb_write did not map onto op=write")
	}
	// No _sum: ECS does not serve one, and inventing it would be a lie.
	if _, ok := findSample(samples, "ecs_node_transaction_latency_milliseconds_sum"); ok {
		t.Error("a _sum was emitted; the store serves none")
	}
}

func TestFluxLatencyBucketLabelKeyOrder(t *testing.T) {
	// One name, one ordered label-key set (ADR-0006).
	f := Flux{now: func() time.Time { return captureInstant }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{
		"statDataHead_performance_internal_latency": "flux_latency.json",
	}))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := findSample(samples, "ecs_node_transaction_latency_milliseconds_bucket")
	got := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		got[i] = l.Key
	}
	if !slices.Equal(got, []string{"node", "op", "le"}) {
		t.Errorf("label keys = %v, want [node op le]", got)
	}
}

func TestFluxLatencyIgnoresUnknownIDs(t *testing.T) {
	// An id the mapping does not cover would otherwise land under a short label
	// set and break the name's schema.
	q := fluxQuery{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_latency",
		perNode: false,
		buckets: &fluxBuckets{
			name:     "ecs_node_transaction_latency_milliseconds",
			idLabels: map[string]string{"ttfb_read": "read", "ttlb_write": "write"},
		},
	}
	rows := []fluxRow{{cols: map[string]string{
		"_field": "1.0", "_value": "5", "_time": captureInstant.Format(time.RFC3339), "id": "ttlb_read",
	}}}
	out, _, _ := q.samples(rows, nil, captureInstant)
	if len(out) != 0 {
		t.Errorf("an unmapped id produced %d samples, want none", len(out))
	}
}

// TestFluxLatencyGroupIsAllOrNothing proves the group-suppression ruling: a
// (node, op) bucket family is emitted in full or not at all. Each bucket
// bound is its own Flux series with its own _time, so nothing at the row
// level otherwise stops one bound going stale, unparseable, or unmapped while
// its siblings survive -- which would leave a silent hole a histogram_quantile
// query cannot detect. Each subtest breaks the supr01-r01 group via exactly
// one of the four independent drop causes and checks two things at once: the
// broken group produces nothing, and an entirely intact sibling group
// (supr01-r02) is unaffected and still emits every bound plus _count.
func TestFluxLatencyGroupIsAllOrNothing(t *testing.T) {
	fresh := captureInstant.Format(time.RFC3339)
	stale := captureInstant.Add(-time.Hour).Format(time.RFC3339)
	mapper := &nodeMapper{byKey: map[string]string{
		"supr01-r01": "supr01-r01",
		"supr01-r02": "supr01-r02",
	}}
	buckets := &fluxBuckets{
		name:     "ecs_node_transaction_latency_milliseconds",
		idLabels: map[string]string{"ttfb_read": "read"},
	}
	q := fluxQuery{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_latency",
		perNode: true, buckets: buckets,
	}
	// healthyGroup is the intact sibling every subtest carries alongside its
	// broken one, proving suppression is scoped to the offending group.
	healthyGroup := []fluxRow{
		{cols: map[string]string{"_field": "0.0", "_value": "1", "_time": fresh, "host": "supr01-r02", "id": "ttfb_read"}},
		{cols: map[string]string{"_field": "+Inf", "_value": "9", "_time": fresh, "host": "supr01-r02", "id": "ttfb_read"}},
	}

	for _, tc := range []struct {
		name                    string
		host                    string // raw host tag shared by every row in the broken group
		broken                  func(host string) fluxRow
		wantStale, wantUnmapped float64
	}{
		{
			name: "stale sibling row",
			host: "supr01-r01",
			broken: func(host string) fluxRow {
				return fluxRow{cols: map[string]string{"_field": "+Inf", "_value": "9", "_time": stale, "host": host, "id": "ttfb_read"}}
			},
			wantStale: 1,
		},
		{
			name: "unparseable value",
			host: "supr01-r01",
			broken: func(host string) fluxRow {
				return fluxRow{cols: map[string]string{"_field": "+Inf", "_value": "not-a-number", "_time": fresh, "host": host, "id": "ttfb_read"}}
			},
		},
		{
			// Every row for this host fails to resolve, including the "0.0" row
			// below built from the same host -- an unresolvable host is a property
			// of the group's identity, not of one row within it.
			name: "unresolvable host",
			host: "unknown-host.example.com",
			broken: func(host string) fluxRow {
				return fluxRow{cols: map[string]string{"_field": "+Inf", "_value": "9", "_time": fresh, "host": host, "id": "ttfb_read"}}
			},
			wantUnmapped: 2,
		},
		{
			name: "missing _field",
			host: "supr01-r01",
			broken: func(host string) fluxRow {
				return fluxRow{cols: map[string]string{"_value": "9", "_time": fresh, "host": host, "id": "ttfb_read"}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := append([]fluxRow{
				{cols: map[string]string{"_field": "0.0", "_value": "1", "_time": fresh, "host": tc.host, "id": "ttfb_read"}},
				tc.broken(tc.host),
			}, healthyGroup...)

			out, unmapped, stale := q.samples(rows, mapper, captureInstant)

			if _, ok := findSample(out, "ecs_node_transaction_latency_milliseconds_bucket", Label{"node", "supr01-r01"}); ok {
				t.Error("the group with one broken row emitted samples; want it fully suppressed")
			}
			if _, ok := findSample(out, "ecs_node_transaction_latency_milliseconds_count", Label{"node", "supr01-r01"}); ok {
				t.Error("the group with one broken row emitted _count; want it fully suppressed")
			}
			if _, ok := findSample(out, "ecs_node_transaction_latency_milliseconds_bucket",
				Label{"node", "supr01-r02"}, Label{"le", "0.0"}); !ok {
				t.Error("the intact sibling group's 0.0 bucket is missing")
			}
			if _, ok := findSample(out, "ecs_node_transaction_latency_milliseconds_bucket",
				Label{"node", "supr01-r02"}, Label{"le", "+Inf"}); !ok {
				t.Error("the intact sibling group's +Inf bucket is missing")
			}
			if _, ok := findSample(out, "ecs_node_transaction_latency_milliseconds_count", Label{"node", "supr01-r02"}); !ok {
				t.Error("the intact sibling group's _count is missing")
			}
			if stale != tc.wantStale {
				t.Errorf("stale = %v, want %v", stale, tc.wantStale)
			}
			if unmapped != tc.wantUnmapped {
				t.Errorf("unmapped = %v, want %v", unmapped, tc.wantUnmapped)
			}
		})
	}
}

// TestFluxLatencyGroupWithoutInfBoundIsSuppressed covers a group that never
// received a +Inf row at all, as opposed to one that received it and had it
// dropped -- g.bad already covers the latter, but a series simply absent from
// the response left the group "good" and let a holed bucket set through: three
// bound rows with no +Inf produced 3 _bucket samples, 0 _count, and no
// suppression. A _bucket family with no _count is malformed, and a missing
// intermediate bound would let histogram_quantile interpolate across the
// wrong boundaries and return a plausible wrong number, so this family is
// suppressed all-or-nothing per series (owner ruling) on a missing +Inf bound
// too, not just on a dropped row.
func TestFluxLatencyGroupWithoutInfBoundIsSuppressed(t *testing.T) {
	fresh := captureInstant.Format(time.RFC3339)
	mapper := &nodeMapper{byKey: map[string]string{"supr01-r01": "supr01-r01"}}
	q := fluxQuery{
		bucket: "monitoring_main", measurement: "statDataHead_performance_internal_latency",
		perNode: true,
		buckets: &fluxBuckets{
			name:     "ecs_node_transaction_latency_milliseconds",
			idLabels: map[string]string{"ttfb_read": "read"},
		},
	}
	// Three finite bounds, no +Inf row at all -- the live capture writes all
	// ten bounds every group, so this shape is unconfirmed against real
	// cluster behaviour; it is a guard against a shape the store has not been
	// observed to produce.
	rows := []fluxRow{
		{cols: map[string]string{"_field": "0.0", "_value": "1", "_time": fresh, "host": "supr01-r01", "id": "ttfb_read"}},
		{cols: map[string]string{"_field": "1.0", "_value": "3", "_time": fresh, "host": "supr01-r01", "id": "ttfb_read"}},
		{cols: map[string]string{"_field": "5.0", "_value": "5", "_time": fresh, "host": "supr01-r01", "id": "ttfb_read"}},
	}

	out, _, _ := q.samples(rows, mapper, captureInstant)

	if len(out) != 0 {
		t.Errorf("a group missing its +Inf bound emitted %d samples, want 0 (fully suppressed)", len(out))
	}
}

func TestFluxDropsStaleFixtureRows(t *testing.T) {
	// The same fixture, read an hour later: every row is older than fluxMaxAge,
	// so the collector must publish nothing rather than an hour-old CPU reading
	// that Prometheus will stamp as current.
	f := Flux{now: func() time.Time { return captureInstant.Add(time.Hour) }}
	samples, err := f.Collect(t.Context(), fluxMock(t, map[string]string{"cpu": "flux_cpu.json"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_node_cpu_utilization_percent"); ok {
		t.Error("an hour-old point was published as a live gauge")
	}
}

// fluxFixtureByMeasurement maps every measurement fluxQueries knows about onto
// the real capture that answers it, so a test can hold exactly one back and
// make it -- and only it -- the run's silent measurement. fluxQueries carries
// ten entries; leaving nine of them without a fixture (as a bare
// {"cpu": "flux_cpu.json"} map would) makes "warned once" and "the same
// silent measurement" impossible to state cleanly, since nine different
// measurements would each independently warn on their own first cycle.
var fluxFixtureByMeasurement = map[string]string{
	"cpu":                             "flux_cpu.json",
	"mem":                             "flux_mem.json",
	"net":                             "flux_net.json",
	"dtquery_dt_status":               "flux_dt_status.json",
	"dtquery_dt_dist_host_dt_node_id": "flux_dt_dist.json",
	"statDataHead_performance_internal_transactions": "flux_transactions.json",
	"statDataHead_performance_internal_throughput":   "flux_throughput.json",
	"statDataHead_performance_internal_latency":      "flux_latency.json",
	"cq_performance_transaction":                     "flux_cq_transaction.json",
	"cq_performance_throughput":                      "flux_cq_throughput.json",
}

// fluxFixturesExcept returns a fixture map covering every measurement
// fluxQueries currently defines except the named one, which is left to fall
// through fluxMock's default "cluster does not carry this" empty response.
// Passing "" excludes nothing, so every measurement answers. It fails the
// test outright if fluxQueries ever grows a measurement this table does not
// know about, rather than silently leaving more than the intended one silent.
func fluxFixturesExcept(t *testing.T, silent string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(fluxQueries))
	for _, q := range fluxQueries {
		if q.measurement == silent {
			continue
		}
		fx, ok := fluxFixtureByMeasurement[q.measurement]
		if !ok {
			t.Fatalf("no fixture registered for measurement %q; add one to fluxFixtureByMeasurement", q.measurement)
		}
		m[q.measurement] = fx
	}
	return m
}

// silenceHook counts the Warn and Debug log entries Flux.Collect emits about a
// measurement returning no rows, so a test can assert on how many of each
// fired without parsing log output.
type silenceHook struct {
	warns, debugs int
}

func (h *silenceHook) Levels() []log.Level {
	return []log.Level{log.WarnLevel, log.DebugLevel}
}

func (h *silenceHook) Fire(e *log.Entry) error {
	if !strings.Contains(e.Message, "no rows") {
		return nil
	}
	switch e.Level {
	case log.WarnLevel:
		h.warns++
	case log.DebugLevel:
		h.debugs++
	}
	return nil
}

// installLogHook installs h on the shared standard logger for the duration of
// the test and restores the prior state afterward. Factored out of
// withSilenceHook so a second, differently-shaped hook (entryCaptureHook,
// below) can reuse the same install/restore discipline instead of duplicating
// it.
//
// Two things a naive install-and-defer-cleanup would get wrong:
//
//  1. Logrus never fires a hook for a level the logger is not currently
//     emitting, and the default level is Info -- so a Debug entry would never
//     reach the hook unless the level is raised for the test.
//  2. log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) as a cleanup
//     would wipe every hook on the shared standard logger, not just the one
//     this test added. Nothing else in this suite installs a hook today, but
//     that makes it a trap for whoever adds one next rather than a safe
//     pattern to copy. ReplaceHooks both installs and returns the displaced
//     value, so the prior hooks are saved and restored exactly, and the prior
//     level is restored the same way.
func installLogHook(t *testing.T, h log.Hook) {
	t.Helper()
	prevLevel := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() { log.SetLevel(prevLevel) })

	prevHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	log.AddHook(h)
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(prevHooks) })
}

// withSilenceHook installs a silenceHook on the shared standard logger for
// the duration of the test and returns it.
func withSilenceHook(t *testing.T) *silenceHook {
	t.Helper()
	var hook silenceHook
	installLogHook(t, &hook)
	return &hook
}

// entryCaptureHook records every log entry fired while installed, fields and
// all, so a test can assert on specific field values rather than merely
// counting occurrences of a message substring (contrast silenceHook, above).
type entryCaptureHook struct {
	entries []log.Entry
}

func (h *entryCaptureHook) Levels() []log.Level { return log.AllLevels }

func (h *entryCaptureHook) Fire(e *log.Entry) error {
	// e.Data is the same map instance across the entry's lifetime; a later
	// WithFields call elsewhere builds a new Entry rather than mutating this
	// one, but the map is still copied defensively so this hook's stored
	// snapshot can never be altered by anything else.
	data := make(log.Fields, len(e.Data))
	for k, v := range e.Data {
		data[k] = v
	}
	h.entries = append(h.entries, log.Entry{Message: e.Message, Level: e.Level, Data: data})
	return nil
}

// TestFluxDebugLine proves the per-measurement accounting line Collect emits
// after mapping a measurement's rows -- rows read, samples emitted, rows
// dropped unmapped, rows dropped stale. Without it, --trace's per-request log
// line (attributed to a measurement by Task 9's other half, in
// internal/ecsclient) has no matching per-measurement summary to correlate
// against, and `stale` in particular has had nothing consuming it since
// Task 3 introduced it.
func TestFluxDebugLine(t *testing.T) {
	hook := &entryCaptureHook{}
	installLogHook(t, hook)

	// Derived from the fixture itself, not hardcoded, so a fixture edit cannot
	// silently desync this test from what Collect actually read.
	var resp fluxResp
	if err := json.Unmarshal([]byte(fixture(t, "flux_cpu.json")), &resp); err != nil {
		t.Fatal(err)
	}
	wantRows := len(resp.rows())

	collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})

	var found *log.Entry
	for i := range hook.entries {
		e := &hook.entries[i]
		if e.Message == "Flux measurement collected" && e.Data["measurement"] == "cpu" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatalf(`no "Flux measurement collected" entry for cpu; entries: %+v`, hook.entries)
	}
	for _, field := range []string{"bucket", "measurement", "rows", "samples", "unmapped", "stale"} {
		if _, ok := found.Data[field]; !ok {
			t.Errorf("debug entry missing field %q: %v", field, found.Data)
		}
	}
	if got := found.Data["rows"]; got != wantRows {
		t.Errorf("rows = %v, want %v (the cpu fixture's actual row count)", got, wantRows)
	}
}

func TestFluxWarnsOncePerSilentMeasurement(t *testing.T) {
	// A measurement the cluster legitimately does not carry would otherwise warn
	// on every cycle forever, about something the operator cannot fix.
	hook := withSilenceHook(t)

	f := Flux{now: func() time.Time { return captureInstant }, silent: &silenceSet{}}
	c := fluxMock(t, fluxFixturesExcept(t, "mem"))
	for range 3 {
		if _, err := f.Collect(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	if hook.warns != 1 {
		t.Errorf("warned %d times for the same silent measurement, want 1", hook.warns)
	}
	if hook.debugs == 0 {
		t.Error("later cycles logged nothing at debug")
	}
}

func TestFluxNilSilentAlwaysWarns(t *testing.T) {
	// A nil *silenceSet must be safe and behave as "always first time": several
	// existing tests build Flux{now: ...} without setting silent at all, and
	// they must keep working exactly as before this change.
	hook := withSilenceHook(t)

	f := Flux{now: func() time.Time { return captureInstant }} // silent left nil
	c := fluxMock(t, fluxFixturesExcept(t, "mem"))
	for range 3 {
		if _, err := f.Collect(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	if hook.warns != 3 {
		t.Errorf("a nil silence set warned %d times over 3 cycles, want 3 (no memory across cycles)", hook.warns)
	}
	if hook.debugs != 0 {
		t.Errorf("a nil silence set logged %d debug entries, want 0", hook.debugs)
	}
}

// TestFluxScriptsMatchesTheRealTable proves FluxScripts replays fluxQueries
// itself rather than a hand-written copy: a capture of queries the exporter
// does not issue proves nothing about the ones it does. Every entry is keyed
// bucket/measurement and its script carries the bucket, the measurement, the
// range, and the trailing |> last() that makes it a single-point read.
func TestFluxScriptsMatchesTheRealTable(t *testing.T) {
	got := FluxScripts()
	if len(got) != len(fluxQueries) {
		t.Fatalf("FluxScripts returned %d entries, want %d (one per fluxQueries entry)", len(got), len(fluxQueries))
	}
	for _, q := range fluxQueries {
		key := q.bucket + "/" + q.measurement
		script, ok := got[key]
		if !ok {
			t.Fatalf("FluxScripts missing key %q", key)
		}
		for _, want := range []string{
			fmt.Sprintf("bucket:%q", q.bucket),
			fmt.Sprintf("r._measurement == %q", q.measurement),
			fmt.Sprintf("range(start: %s)", fluxRange),
			"|> last()",
		} {
			if !strings.Contains(script, want) {
				t.Errorf("%s: script missing %q:\n%s", key, want, script)
			}
		}
		// FluxScripts must render exactly what the collector itself would send.
		if script != q.script() {
			t.Errorf("%s: FluxScripts diverged from q.script()", key)
		}
	}
}

// TestFluxScriptForRendersTheSameShape covers the free-form probing path
// (--bucket/--measurement), which builds a fluxQuery the table does not carry.
func TestFluxScriptForRendersTheSameShape(t *testing.T) {
	script := FluxScriptFor("monitoring_vdc", "cq_performance_transaction")
	for _, want := range []string{
		`bucket:"monitoring_vdc"`,
		`r._measurement == "cq_performance_transaction"`,
		fmt.Sprintf("range(start: %s)", fluxRange),
		"|> last()",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("FluxScriptFor script missing %q:\n%s", want, script)
		}
	}
}

func TestFluxSilentMeasurementReannouncesAfterAnswering(t *testing.T) {
	// A measurement that starts answering again is forgotten, so a later
	// disappearance must warn afresh rather than staying silenced forever. This
	// is the round trip TestFluxWarnsOncePerSilentMeasurement alone cannot show,
	// since that test only ever sees mem silent.
	hook := withSilenceHook(t)
	f := Flux{now: func() time.Time { return captureInstant }, silent: &silenceSet{}}

	// Cycle 1: mem is silent for the first time, so it warns.
	silentBodies := fluxFixturesExcept(t, "mem")
	if _, err := f.Collect(t.Context(), fluxMock(t, silentBodies)); err != nil {
		t.Fatal(err)
	}
	if hook.warns != 1 {
		t.Fatalf("after the first silent cycle, warns = %d, want 1", hook.warns)
	}

	// Cycle 2: mem answers. It must be forgotten, not merely skipped once.
	answeringBodies := fluxFixturesExcept(t, "")
	if _, err := f.Collect(t.Context(), fluxMock(t, answeringBodies)); err != nil {
		t.Fatal(err)
	}
	if hook.warns != 1 {
		t.Fatalf("a cycle where mem answered must not warn: warns = %d, want 1", hook.warns)
	}

	// Cycle 3: mem goes silent again. Having been forgotten in cycle 2, this
	// must warn afresh rather than staying silenced from cycle 1.
	if _, err := f.Collect(t.Context(), fluxMock(t, silentBodies)); err != nil {
		t.Fatal(err)
	}
	if hook.warns != 2 {
		t.Errorf("mem's reappearing silence did not warn afresh: warns = %d, want 2", hook.warns)
	}
}
