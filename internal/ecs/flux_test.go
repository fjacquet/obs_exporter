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
			e.fluxClient.queries = append(e.fluxClient.queries, q["query"])
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
	c.fluxClient.postErr = errors.New("500 Internal Server Error")
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
