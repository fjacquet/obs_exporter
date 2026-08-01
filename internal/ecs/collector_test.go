package ecs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

func boolPtr(b bool) *bool { return &b }

func testTargets(t *testing.T) []Target {
	t.Helper()
	cl := config.Cluster{Name: "test-cluster", CollectMetering: boolPtr(true)}
	return []Target{{Client: mockClient(t), Collectors: Registry(cl)}}
}

func TestCollectOnce(t *testing.T) {
	store := NewSnapshotStore()
	cycles := 0
	col := NewCollector(testTargets(t), store, time.Minute, 10*time.Second)
	col.PostCycle = func() { cycles++ }

	snap := col.CollectOnce(context.Background())
	if store.Load() != snap {
		t.Fatal("snapshot not stored")
	}
	if cycles != 1 {
		t.Errorf("PostCycle ran %d times, want 1", cycles)
	}
	if len(snap.Clusters) != 1 {
		t.Fatalf("clusters = %d", len(snap.Clusters))
	}
	cs := snap.Clusters[0]
	if !cs.OK {
		t.Fatalf("cluster not OK: %s", cs.Err)
	}

	cluster := Label{"cluster", "test-cluster"}
	mustSample(t, cs.Samples, "ecs_up", 1, cluster)
	for _, rc := range []string{"cluster", "replication", "nodes", "info", "metering"} {
		mustSample(t, cs.Samples, "ecs_collector_up", 1, cluster, Label{"collector", rc})
	}
	// Domain samples are stamped with the cluster identity label.
	mustSample(t, cs.Samples, "ecs_cluster_nodes", 4, cluster, Label{"state", "good"})
	mustSample(t, cs.Samples, "ecs_namespace_objects", 8, cluster, Label{"namespace", "s3"})
}

func TestCollectClusterAllFailed(t *testing.T) {
	failing := &ecsclient.Mock{
		ClusterName: "broken",
		Responses:   map[string]string{},
	}
	cl := config.Cluster{Name: "broken", CollectMetering: boolPtr(false)}
	store := NewSnapshotStore()
	col := NewCollector([]Target{{Client: failing, Collectors: Registry(cl)}}, store, time.Minute, 10*time.Second)

	snap := col.CollectOnce(context.Background())
	cs := snap.Clusters[0]
	if cs.OK {
		t.Fatal("cluster should not be OK")
	}
	cluster := Label{"cluster", "broken"}
	mustSample(t, cs.Samples, "ecs_up", 0, cluster)
	mustSample(t, cs.Samples, "ecs_collector_up", 0, cluster, Label{"collector", "cluster"})
}

func TestCollectClusterPartialFailure(t *testing.T) {
	m := mockClient(t)
	m.Errs = map[string]error{pathReplicationGroups: errors.New("boom")}
	cl := config.Cluster{Name: "test-cluster"}
	store := NewSnapshotStore()
	col := NewCollector([]Target{{Client: m, Collectors: Registry(cl)}}, store, time.Minute, 10*time.Second)

	cs := col.CollectOnce(context.Background()).Clusters[0]
	if !cs.OK {
		t.Fatalf("partial failure should keep cluster OK: %s", cs.Err)
	}
	cluster := Label{"cluster", "test-cluster"}
	mustSample(t, cs.Samples, "ecs_up", 1, cluster)
	mustSample(t, cs.Samples, "ecs_collector_up", 0, cluster, Label{"collector", "replication"})
	mustSample(t, cs.Samples, "ecs_collector_up", 1, cluster, Label{"collector", "cluster"})
}

// emptyCollector always succeeds with no domain samples, modeling "every
// other collector returned an empty success" — the scenario that let Flux's
// always-present housekeeping counter alone keep ecs_up at 1.
type emptyCollector struct{ name string }

func (e emptyCollector) Name() string { return e.name }
func (e emptyCollector) Collect(context.Context, ecsclient.Client) ([]Sample, error) {
	return nil, nil
}

// TestCollectClusterFluxHousekeepingDoesNotCountAsDomainSample guards against
// ecs_collector_unmapped_nodes{collector="flux"} — emitted every cycle,
// including as 0 — being counted toward domainSamples in collectCluster. That
// housekeeping sample alone must not be able to keep ecs_up at 1 when every
// other collector, and every Flux measurement, produced nothing.
func TestCollectClusterFluxHousekeepingDoesNotCountAsDomainSample(t *testing.T) {
	client := fluxMock(t, nil) // every Flux measurement answers 200 with no rows
	target := Target{
		Client: client,
		Collectors: []ResourceCollector{
			emptyCollector{name: "cluster"},
			Flux{},
		},
	}
	store := NewSnapshotStore()
	col := NewCollector([]Target{target}, store, time.Minute, 10*time.Second)
	cs := col.CollectOnce(context.Background()).Clusters[0]

	if cs.OK {
		t.Fatalf("cluster should not be OK when only housekeeping samples were collected: err=%s", cs.Err)
	}
	cluster := Label{"cluster", "test-cluster"}
	mustSample(t, cs.Samples, "ecs_up", 0, cluster)
	mustSample(t, cs.Samples, "ecs_collector_unmapped_nodes", 0, cluster, Label{"collector", "flux"})
}

// assertLabelKeySchema enforces the family label-key invariant (ADR-0006):
// every sample of a given metric name must carry the same ordered label-key
// set. Shared by TestLabelKeyConsistency and TestLabelKeyConsistencyFlux so a
// violation anywhere in either fixture set fails the same way.
func assertLabelKeySchema(t *testing.T, samples []Sample) {
	t.Helper()
	schema := map[string][]string{}
	for _, s := range samples {
		keys := make([]string, len(s.Labels))
		for i, l := range s.Labels {
			keys[i] = l.Key
		}
		if want, ok := schema[s.Name]; ok {
			if len(want) != len(keys) {
				t.Errorf("metric %s has inconsistent label keys: %v vs %v", s.Name, want, keys)
				continue
			}
			for i := range want {
				if want[i] != keys[i] {
					t.Errorf("metric %s has inconsistent label keys: %v vs %v", s.Name, want, keys)
					break
				}
			}
		} else {
			schema[s.Name] = keys
		}
	}
}

// TestLabelKeyConsistency enforces the family label-key invariant: every sample of
// a given metric name must carry the same ordered label-key set, across all
// collectors and clusters, so dashboards never see mixed series schemas.
func TestLabelKeyConsistency(t *testing.T) {
	store := NewSnapshotStore()
	col := NewCollector(testTargets(t), store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	var samples []Sample
	for _, cs := range snap.Clusters {
		samples = append(samples, cs.Samples...)
	}
	assertLabelKeySchema(t, samples)
}

// TestLabelKeyConsistencyFlux extends the same guard to the Flux collector.
// testTargets (above) builds its cluster with CollectFlux left at its zero
// value, so Registry never appends Flux{} there and none of its samples are
// ever checked. This test wires a cluster with CollectFlux: true through the
// same Registry/Collector path, using flux_test.go's measurement-routing mock
// to feed the multi-key net measurement — ecs_node_network_bytes_total carries
// {node, interface, direction} in that order from a static query table today;
// nothing else in the suite would catch an edit that made the order depend on
// row data instead. The fixture map also covers the bucket-mode latency
// histogram (the only family carrying an "le" label) and per-node
// ecs_node_dt_total, so both new label-key sets this branch adds are checked
// here too — previously neither reached this test at all.
func TestLabelKeyConsistencyFlux(t *testing.T) {
	cl := config.Cluster{Name: "test-cluster", CollectFlux: true, CollectMetering: boolPtr(false)}
	client := fluxMock(t, map[string]string{
		"cpu":                             "flux_cpu.json",
		"mem":                             "flux_mem.json",
		"net":                             "flux_net.json",
		"dtquery_dt_status":               "flux_dt_status.json",
		"dtquery_dt_dist_host_dt_node_id": "flux_dt_dist.json",
		"statDataHead_performance_internal_latency":      "flux_latency.json",
		"statDataHead_performance_internal_transactions": "flux_transactions.json",
		"statDataHead_performance_internal_throughput":   "flux_throughput.json",
	})
	// Registry builds Flux{} with the real clock; pin it to the fixtures'
	// capture instant (flux_test.go) so their _time values read as fresh
	// rather than all being dropped as stale.
	collectors := Registry(cl)
	for i, rc := range collectors {
		if _, ok := rc.(Flux); ok {
			collectors[i] = Flux{now: func() time.Time { return captureInstant }}
		}
	}
	store := NewSnapshotStore()
	col := NewCollector([]Target{{Client: client, Collectors: collectors}}, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	cs := snap.Clusters[0]
	if !cs.OK {
		t.Fatalf("cluster not OK: %s", cs.Err)
	}
	assertLabelKeySchema(t, cs.Samples)

	// Guard against the schema check passing vacuously because Flux's samples
	// never made it into cs.Samples. collectCluster stamps every domain sample
	// with the cluster identity label first (Sample.WithCluster), so it leads
	// the key order here too.
	s, ok := findSample(cs.Samples, "ecs_node_network_bytes_total",
		Label{"cluster", "test-cluster"}, Label{"node", "supr01-r01"}, Label{"interface", "public"}, Label{"direction", "received"})
	if !ok {
		t.Fatal("ecs_node_network_bytes_total not collected; schema check ran on nothing from Flux")
	}
	wantKeys := []string{"cluster", "node", "interface", "direction"}
	gotKeys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		gotKeys[i] = l.Key
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("label keys = %v, want %v", gotKeys, wantKeys)
	}
}

func TestRegistryArbitratesPerfNamesWithFlux(t *testing.T) {
	// The three names below exist in both sources. Exactly one collector may own
	// each per cycle, and the decision is made here, before any request goes out.
	off := Registry(config.Cluster{})
	on := Registry(config.Cluster{CollectFlux: true})

	var offNodes, onNodes Nodes
	for _, rc := range off {
		if n, ok := rc.(Nodes); ok {
			offNodes = n
		}
	}
	for _, rc := range on {
		if n, ok := rc.(Nodes); ok {
			onNodes = n
		}
	}
	if offNodes.FluxOwnsPerf {
		t.Error("Nodes must keep the perf names when Flux is off")
	}
	if !onNodes.FluxOwnsPerf {
		t.Error("Nodes must yield the perf names when Flux is on")
	}

	var hasFlux bool
	for _, rc := range on {
		if rc.Name() == "flux" {
			hasFlux = true
		}
	}
	if !hasFlux {
		t.Error("collectFlux must register the flux collector")
	}
	for _, rc := range off {
		if rc.Name() == "flux" {
			t.Error("flux must not be registered when the flag is unset")
		}
	}
}

func TestRegistryGivesDTOwnershipToTheDTCollector(t *testing.T) {
	both := Registry(config.Cluster{CollectFlux: true, CollectDT: true})
	fluxOnly := Registry(config.Cluster{CollectFlux: true})
	find := func(rcs []ResourceCollector) Flux {
		t.Helper()
		for _, rc := range rcs {
			if f, ok := rc.(Flux); ok {
				return f
			}
		}
		t.Fatal("no Flux collector in the registry")
		return Flux{}
	}
	if !find(both).DTOwnedByDT {
		t.Error("with collectDT on, the DT collector must own the per-node name")
	}
	if find(fluxOnly).DTOwnedByDT {
		t.Error("with collectDT off, Flux must own the per-node name")
	}
}

func labelKeys(s Sample) []string {
	keys := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		keys[i] = l.Key
	}
	return keys
}

func TestCollectStampsCustomLabels(t *testing.T) {
	targets := testTargets(t)
	targets[0].Labels = []Label{{Key: "env", Value: "prod"}, {Key: "site", Value: "geneva"}}

	store := NewSnapshotStore()
	col := NewCollector(targets, store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	var checked int
	for _, s := range snap.Clusters[0].Samples {
		if s.LabelValue("env") != "prod" || s.LabelValue("site") != "geneva" {
			t.Fatalf("sample %s missing custom labels: %v", s.Name, s.Labels)
		}
		if s.Name == "ecs_up" || s.Name == "ecs_collector_up" {
			checked++
			want := []string{"cluster", "env", "site"}
			if s.Name == "ecs_collector_up" {
				want = append(want, "collector")
			}
			if !slices.Equal(labelKeys(s), want) {
				t.Errorf("%s label keys = %v, want %v", s.Name, labelKeys(s), want)
			}
		}
	}
	if checked == 0 {
		t.Fatal("neither ecs_up nor ecs_collector_up was present")
	}
	assertLabelKeySchema(t, snap.Clusters[0].Samples)
}

func TestCollectWarnsOnceOnLabelCollision(t *testing.T) {
	hook := test.NewGlobal()
	defer hook.Reset()

	targets := testTargets(t)
	targets[0].Labels = []Label{{Key: "collector", Value: "mine"}}

	store := NewSnapshotStore()
	col := NewCollector(targets, store, time.Minute, 10*time.Second)
	col.CollectOnce(context.Background())
	col.CollectOnce(context.Background())

	var warnings int
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "custom label collides") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("collision warnings = %d over two cycles, want 1", warnings)
	}

	snap := store.Load()
	for _, s := range snap.Clusters[0].Samples {
		if s.Name == "ecs_collector_up" && s.LabelValue("collector") == "mine" {
			t.Fatal("custom label overwrote the collector dimension")
		}
	}
}

func TestNodesYieldsArbitratedNames(t *testing.T) {
	samples, err := Nodes{FluxOwnsPerf: true}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"ecs_node_cpu_utilization_percent",
		"ecs_node_memory_utilization_percent",
		"ecs_node_memory_used_bytes",
	} {
		if _, ok := findSample(samples, name); ok {
			t.Errorf("%s emitted by Nodes while Flux owns it", name)
		}
	}
	// Names Flux cannot fill — its net measurement carries a per-interface
	// dimension, so it must use a different name — stay with the dashboard.
	if _, ok := findSample(samples, "ecs_node_nic_bandwidth"); !ok {
		t.Error("nic_bandwidth must stay on the dashboard path")
	}
}
