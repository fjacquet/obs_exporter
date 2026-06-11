package ecs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
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
	mustSample(t, cs.Samples, "ecs_cluster_good_nodes", 4, cluster)
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

// TestLabelKeyConsistency enforces the family label-key invariant: every sample of
// a given metric name must carry the same ordered label-key set, across all
// collectors and clusters, so dashboards never see mixed series schemas.
func TestLabelKeyConsistency(t *testing.T) {
	store := NewSnapshotStore()
	col := NewCollector(testTargets(t), store, time.Minute, 10*time.Second)
	snap := col.CollectOnce(context.Background())

	schema := map[string][]string{}
	for _, cs := range snap.Clusters {
		for _, s := range cs.Samples {
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
}
