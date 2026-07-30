package ecs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

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
	fail   bool
}

func (f *fluxClient) Post(_ context.Context, path string, body, out any) error {
	if f.fail {
		return errors.New("401 Unauthorized")
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
	samples, err := Flux{}.Collect(t.Context(), fluxMock(t, byMeasurement))
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestFluxCollectPerNodeGauges(t *testing.T) {
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 31.5, Label{"node", "supr01-r01"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 12.25, Label{"node", "supr01-r02"})
	s, _ := findSample(samples, "ecs_node_cpu_utilization_percent", Label{"node", "supr01-r01"})
	if s.Type != Gauge {
		t.Error("cpu utilization must be a gauge")
	}
}

func TestFluxCollectNetworkCounters(t *testing.T) {
	samples := collectFlux(t, map[string]string{"net": "flux_net.json"})
	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_network_bytes_total", 994013184, n1, Label{"interface", "eth0"}, Label{"direction", "received"})
	mustSample(t, samples, "ecs_node_network_bytes_total", 551944704, n1, Label{"interface", "eth0"}, Label{"direction", "transmitted"})

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
	mustSample(t, samples, "ecs_cluster_dt_total", 128)
	mustSample(t, samples, "ecs_cluster_dt_unready", 2)
	mustSample(t, samples, "ecs_cluster_dt_unknown", 1)
	s, _ := findSample(samples, "ecs_cluster_dt_total")
	if len(s.Labels) != 0 {
		t.Errorf("cluster DT carries labels %v, want none", s.Labels)
	}
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
	if _, err := (Flux{}).Collect(t.Context(), &fluxClient{Client: c, bodies: nil, t: t, fail: true}); err == nil {
		t.Error("Collect must return an error when the Flux endpoint rejects the query")
	}
}

func TestFluxCollectSurvivesRenamedMeasurement(t *testing.T) {
	// Measurement names are undocumented and unversioned: net/utilization is
	// listed in 3.8 and gone in 4.3. One missing measurement must not take the
	// other seven with it.
	samples := collectFlux(t, map[string]string{"cpu": "flux_cpu.json"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 31.5, Label{"node", "supr01-r01"})
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
