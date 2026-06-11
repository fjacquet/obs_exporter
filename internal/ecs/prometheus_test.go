package ecs

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
		"ecs_cluster_good_nodes",
		"ecs_cluster_alerts_unacknowledged",
		"ecs_cluster_transaction_errors",
		"ecs_replication_group_ingress_traffic",
		"ecs_node_cpu_utilization_percent",
		"ecs_namespace_used_bytes",
		"ecs_cluster_info",
	} {
		if families[want] == 0 {
			t.Errorf("metric family %s missing from gather", want)
		}
	}
	if got := families["ecs_cluster_alerts_unacknowledged"]; got != 4 {
		t.Errorf("alerts series = %d, want 4 (one per severity)", got)
	}
	if got := families["ecs_node_healthy"]; got != 2 {
		t.Errorf("node healthy series = %d, want 2", got)
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

func TestPromCollectorEmptyStore(t *testing.T) {
	store := NewSnapshotStore()
	if got := len(gather(t, store)); got != 0 {
		t.Errorf("expected empty gather, got %d families", got)
	}
}
