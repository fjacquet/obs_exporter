package ecs

import (
	"context"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

func TestNodesCollect(t *testing.T) {
	samples, err := Nodes{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_healthy", 1, n1)
	// Good node: health_state carries the lowercased status; no other state series.
	mustSample(t, samples, "ecs_node_health_state", 1, n1, Label{"state", "good"})
	if _, ok := findSample(samples, "ecs_node_health_state", n1, Label{"state", "bad"}); ok {
		t.Error("node1 (Good) must not emit ecs_node_health_state{state=bad}")
	}
	mustSample(t, samples, "ecs_node_disks_installed", 40, n1)
	mustSample(t, samples, "ecs_node_disks", 40, n1, Label{"state", "good"})
	mustSample(t, samples, "ecs_node_disk_space_total_bytes", 510, n1)
	mustSample(t, samples, "ecs_node_disk_space_bytes", 90, n1, Label{"type", "free"})
	mustSample(t, samples, "ecs_node_disk_space_bytes", 420, n1, Label{"type", "allocated"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 43, n1)
	mustSample(t, samples, "ecs_node_memory_utilization_percent", 35, n1)
	mustSample(t, samples, "ecs_node_memory_used_bytes", 11000, n1)
	mustSample(t, samples, "ecs_node_nic_bandwidth", 4300, n1, Label{"direction", "received"})
	mustSample(t, samples, "ecs_node_nic_bandwidth", 3009, n1, Label{"direction", "transmitted"})
	mustSample(t, samples, "ecs_node_nic_utilization_percent", 14, n1)
	mustSample(t, samples, "ecs_node_transaction_latency_milliseconds", 9, n1, Label{"op", "read"})
	mustSample(t, samples, "ecs_node_transactions_per_second", 1600, n1, Label{"op", "write"})

	n2 := Label{"node", "supr01-r02"}
	mustSample(t, samples, "ecs_node_healthy", 0, n2)
	mustSample(t, samples, "ecs_node_health_state", 1, n2, Label{"state", "bad"})
	mustSample(t, samples, "ecs_node_disks", 1, n2, Label{"state", "bad"})
	mustSample(t, samples, "ecs_node_disks", 1, n2, Label{"state", "ready_to_replace"})
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 88, n2)
	// node 2 reports no NIC stats: samples must be absent, not zero.
	if _, ok := findSample(samples, "ecs_node_nic_utilization_percent", n2); ok {
		t.Error("nic utilization for node2 should be absent")
	}

	// All five documented healthStatus values (Good, Suspect, Bad, NotAccessible,
	// Maintenance) must round-trip: healthy is 1 only for Good, and health_state
	// carries every state — including the two (Suspect, NotAccessible) the v1
	// Python exporter did not map.
	n3 := Label{"node", "supr01-r03"} // Suspect
	mustSample(t, samples, "ecs_node_healthy", 0, n3)
	mustSample(t, samples, "ecs_node_health_state", 1, n3, Label{"state", "suspect"})

	n4 := Label{"node", "supr01-r04"} // NotAccessible
	mustSample(t, samples, "ecs_node_healthy", 0, n4)
	mustSample(t, samples, "ecs_node_health_state", 1, n4, Label{"state", "notaccessible"})

	n5 := Label{"node", "supr01-r05"} // Maintenance
	mustSample(t, samples, "ecs_node_healthy", 0, n5)
	mustSample(t, samples, "ecs_node_health_state", 1, n5, Label{"state", "maintenance"})
	mustSample(t, samples, "ecs_node_disks", 2, n5, Label{"state", "maintenance"})
}

// TestNodesCollectDocumentedInstancesKey serves the real fixture with the HAL
// array key rewritten to the spelling the Dell reference documents. The
// decoder must tolerate both, so the resulting samples are identical.
//
// The payload is derived from the fixture at test time on purpose: a second
// fixture file could drift from the first, and cmd/mockecs/fixtures/ would
// have to mirror it.
func TestNodesCollectDocumentedInstancesKey(t *testing.T) {
	mc := mockClient(t)
	orig := mc.Responses[pathLocalZoneNodes]
	rewritten := strings.ReplaceAll(orig, `"_instances"`, `"instances"`)
	if rewritten == orig {
		t.Fatal("fixture no longer contains the _instances key; this test would cover nothing")
	}
	mc.Responses[pathLocalZoneNodes] = rewritten

	samples, err := Nodes{}.Collect(context.Background(), mc)
	if err != nil {
		t.Fatal(err)
	}

	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_healthy", 1, n1)
	mustSample(t, samples, "ecs_node_health_state", 1, n1, Label{"state", "good"})
	mustSample(t, samples, "ecs_node_disks_installed", 40, n1)
	mustSample(t, samples, "ecs_node_disk_space_total_bytes", 510, n1)
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 43, n1)

	n2 := Label{"node", "supr01-r02"}
	mustSample(t, samples, "ecs_node_healthy", 0, n2)
	mustSample(t, samples, "ecs_node_health_state", 1, n2, Label{"state", "bad"})
	mustSample(t, samples, "ecs_node_disks", 1, n2, Label{"state", "bad"})
}

// TestNodesCollectUnknownShapeWarns pins the wiring between Collect and
// warnHalShape: deleting the call site should fail this test even
// though TestWarnHalShape covers the helper in isolation.
func TestNodesCollectUnknownShapeWarns(t *testing.T) {
	hook := test.NewGlobal()
	defer hook.Reset()

	mc := mockClient(t)
	mc.Responses[pathLocalZoneNodes] = `{"_embedded":{"_links":{}}}`

	samples, err := Nodes{}.Collect(context.Background(), mc)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 0 {
		t.Fatalf("got %d samples, want 0", len(samples))
	}

	if got := len(hook.Entries); got != 1 {
		t.Fatalf("got %d log entries, want 1", got)
	}
	entry := hook.LastEntry()
	if entry.Level != logrus.WarnLevel {
		t.Errorf("level = %v, want warning", entry.Level)
	}
	if entry.Data["cluster"] != mc.ClusterName {
		t.Errorf("cluster field = %v, want %v", entry.Data["cluster"], mc.ClusterName)
	}
	if entry.Data["path"] != pathLocalZoneNodes {
		t.Errorf("path field = %v, want %v", entry.Data["path"], pathLocalZoneNodes)
	}
}

func TestNodesGivesUpLatencyWhenFluxOwnsIt(t *testing.T) {
	// Prometheus reads X_bucket as belonging to a histogram named X, so the
	// dashboard gauge and the Flux histogram cannot both hold this family.
	with, err := Nodes{FluxOwnsPerf: true}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(with, "ecs_node_transaction_latency_milliseconds"); ok {
		t.Error("Nodes emitted the latency gauge while Flux owns the family")
	}
	// The cluster-level name has no Flux equivalent and is untouched.
	without, err := Nodes{}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(without, "ecs_node_transaction_latency_milliseconds"); !ok {
		t.Error("Nodes stopped emitting the latency gauge with Flux off")
	}
}

// TestNodesKeepsBandwidthAndTPSWhenFluxOwnsPerf pins the suppression's scope:
// it must remove exactly ecs_node_transaction_latency_milliseconds and nothing
// else from transactionFields.samples' output. Flux has no equivalent for
// bandwidth or transactions-per-second, so those two names have no other
// owner and must keep flowing even while FluxOwnsPerf suppresses the latency
// gauge. An over-broad predicate (e.g. a prefix match on
// "ecs_node_transaction") would pass every other test in this package while
// silently dropping both names -- this is the test that catches it.
func TestNodesKeepsBandwidthAndTPSWhenFluxOwnsPerf(t *testing.T) {
	samples, err := Nodes{FluxOwnsPerf: true}.Collect(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findSample(samples, "ecs_node_transaction_bandwidth_mb_per_second"); !ok {
		t.Error("Nodes suppressed the bandwidth family while Flux owns performance; Flux has no equivalent for it")
	}
	if _, ok := findSample(samples, "ecs_node_transactions_per_second"); !ok {
		t.Error("Nodes suppressed the transactions-per-second family while Flux owns performance; Flux has no equivalent for it")
	}
}

func TestInfoCollect(t *testing.T) {
	samples, err := Info{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	mustSample(t, samples, "ecs_cluster_info", 1, Label{"version", "4.1.0.0.12345"})
}
