package ecs

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gather(t *testing.T, store *SnapshotStore) map[string]int {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewPromCollector(store))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, mf := range mfs {
		out[mf.GetName()] = len(mf.GetMetric())
	}
	return out
}

func TestPromCollectorGather(t *testing.T) {
	store := NewSnapshotStore()
	col := NewCollector(testTargets(t), store, time.Minute, 10*time.Second)
	col.CollectOnce(t.Context())

	families := gather(t, store)
	for _, want := range []string{
		"ecs_up",
		"ecs_collector_up",
		"ecs_cluster_nodes",
		"ecs_cluster_nodes_installed",
		"ecs_cluster_alerts_unacknowledged",
		"ecs_cluster_transaction_errors",
		"ecs_cluster_disk_space_bytes",
		"ecs_cluster_disk_space_offline_total_bytes",
		"ecs_cluster_replication_rpo_lag_seconds",
		"ecs_replication_group_traffic",
		"ecs_node_cpu_utilization_percent",
		"ecs_node_health_state",
		"ecs_namespace_used_bytes",
		"ecs_cluster_info",
		"ecs_cluster_gc_bytes",
		"ecs_cluster_gc_enabled",
		"ecs_cluster_recovery_bad_chunks_bytes",
		"ecs_cluster_ec_coded_ratio_percent",
		"ecs_cluster_disk_space_allocated_component_bytes",
	} {
		if families[want] == 0 {
			t.Errorf("metric family %s missing from gather", want)
		}
	}
	if got := families["ecs_cluster_alerts_unacknowledged"]; got != 4 {
		t.Errorf("alerts series = %d, want 4 (one per severity)", got)
	}
	if got := families["ecs_node_healthy"]; got != 5 {
		t.Errorf("node healthy series = %d, want 5", got)
	}
	// Two scopes × {pending, unreclaimable} now share one metric name.
	if got := families["ecs_cluster_gc_bytes"]; got != 4 {
		t.Errorf("gc bytes series = %d, want 4 (scope × state)", got)
	}
	// allocated, free and reserved; the total keeps its own name.
	if got := families["ecs_cluster_disk_space_bytes"]; got != 3 {
		t.Errorf("cluster disk space series = %d, want 3 (allocated, free, reserved)", got)
	}
	if got := families["ecs_cluster_disk_space_total_bytes"]; got != 1 {
		t.Errorf("cluster disk space total series = %d, want 1 (the aggregate is not in the labelled family)", got)
	}
	// The fixture omits gcSystemMetadataIsEnabled, so only scope=user is enabled.
	if got := families["ecs_cluster_gc_enabled"]; got != 1 {
		t.Errorf("gc enabled series = %d, want 1 (the fixture omits the system flag)", got)
	}
	// The fixture omits the two geo components.
	if got := families["ecs_cluster_disk_space_allocated_component_bytes"]; got != 3 {
		t.Errorf("allocation component series = %d, want 3 (the fixture omits the geo purposes)", got)
	}
}

func TestPromCollectorDropsLabelDrift(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{Name: "ecs_drifty", Labels: []Label{{Key: "a", Value: "1"}}, Value: 1},
			{Name: "ecs_drifty", Labels: []Label{{Key: "b", Value: "2"}}, Value: 2},
			{Name: "ecs_drifty", Labels: []Label{{Key: "a", Value: "3"}}, Value: 3},
		},
	}}})
	families := gather(t, store)
	if got := families["ecs_drifty"]; got != 2 {
		t.Errorf("drifting series kept = %d, want 2 (the schema-matching ones)", got)
	}
}

// TestPromCollectorDropsDuplicateSeries guards against the failure mode where
// two samples share both a name and identical label values (not just a
// matching schema). client_golang's registry rejects that as "collected
// metric ... was collected before with the same name and label values", and
// main.go serves promhttp with the zero-value ErrorHandling
// (HTTPErrorOnError), so a single duplicate would 500 the whole /metrics
// endpoint for every cluster rather than drop one series.
func TestPromCollectorDropsDuplicateSeries(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{Name: "ecs_dupe", Labels: []Label{{Key: "a", Value: "1"}}, Value: 1},
			{Name: "ecs_dupe", Labels: []Label{{Key: "a", Value: "1"}}, Value: 2},
		},
	}}})
	families := gather(t, store)
	if got := families["ecs_dupe"]; got != 1 {
		t.Errorf("duplicate series kept = %d, want 1 (the second occurrence dropped)", got)
	}
}

func TestPromCollectorEmptyStore(t *testing.T) {
	store := NewSnapshotStore()
	if got := len(gather(t, store)); got != 0 {
		t.Errorf("expected empty gather, got %d families", got)
	}
}

func TestPromCollectorEmitsCounterType(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{Name: "ecs_node_requests_total", Labels: []Label{{"node", "n1"}}, Value: 5, Type: Counter},
			{Name: "ecs_node_cpu_utilization_percent", Labels: []Label{{"node", "n1"}}, Value: 12},
		},
	}}})

	reg := prometheus.NewRegistry()
	reg.MustRegister(NewPromCollector(store))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]dto.MetricType{}
	for _, mf := range mfs {
		types[mf.GetName()] = mf.GetType()
	}
	if types["ecs_node_requests_total"] != dto.MetricType_COUNTER {
		t.Errorf("requests_total type = %v, want COUNTER", types["ecs_node_requests_total"])
	}
	if types["ecs_node_cpu_utilization_percent"] != dto.MetricType_GAUGE {
		t.Errorf("cpu_utilization type = %v, want GAUGE", types["ecs_node_cpu_utilization_percent"])
	}
}
