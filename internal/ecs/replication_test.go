package ecs

import (
	"context"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

func TestReplicationCollect(t *testing.T) {
	samples, err := Replication{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	rg1 := Label{"rg", "rg_name1"}
	mustSample(t, samples, "ecs_replication_group_ingress_traffic", 12000, rg1)
	mustSample(t, samples, "ecs_replication_group_egress_traffic", 9500, rg1)
	mustSample(t, samples, "ecs_replication_group_chunks_repo_pending_replication_bytes", 500000, rg1)
	mustSample(t, samples, "ecs_replication_group_chunks_journal_pending_replication_bytes", 400000, rg1)
	mustSample(t, samples, "ecs_replication_group_chunks_pending_xor_bytes", 300000, rg1)
	mustSample(t, samples, "ecs_replication_group_rpo_timestamp_seconds", 12345678, rg1)
	mustSample(t, samples, "ecs_replication_group_rpo_lag_seconds", 7200, rg1)
	mustSample(t, samples, "ecs_replication_group_zones", 3, rg1)

	rg2 := Label{"rg", "rg_name2"}
	mustSample(t, samples, "ecs_replication_group_ingress_traffic", 100, rg2)
	mustSample(t, samples, "ecs_replication_group_zones", 2, rg2)
	// rg2 has no replicationRpoLag: the sample must be absent, not zero.
	if _, ok := findSample(samples, "ecs_replication_group_rpo_lag_seconds", rg2); ok {
		t.Error("rpo_lag for rg2 should be absent")
	}
}

// TestReplicationCollectDocumentedInstancesKey serves the real fixture with the
// HAL array key rewritten to the spelling the Dell reference documents. The
// decoder must tolerate both, so the resulting samples are identical.
func TestReplicationCollectDocumentedInstancesKey(t *testing.T) {
	mc := mockClient(t)
	orig := mc.Responses[pathReplicationGroups]
	rewritten := strings.ReplaceAll(orig, `"_instances"`, `"instances"`)
	if rewritten == orig {
		t.Fatal("fixture no longer contains the _instances key; this test would cover nothing")
	}
	mc.Responses[pathReplicationGroups] = rewritten

	samples, err := Replication{}.Collect(context.Background(), mc)
	if err != nil {
		t.Fatal(err)
	}

	rg1 := Label{"rg", "rg_name1"}
	mustSample(t, samples, "ecs_replication_group_ingress_traffic", 12000, rg1)
	mustSample(t, samples, "ecs_replication_group_egress_traffic", 9500, rg1)
	mustSample(t, samples, "ecs_replication_group_rpo_lag_seconds", 7200, rg1)
	mustSample(t, samples, "ecs_replication_group_zones", 3, rg1)

	rg2 := Label{"rg", "rg_name2"}
	mustSample(t, samples, "ecs_replication_group_ingress_traffic", 100, rg2)
	mustSample(t, samples, "ecs_replication_group_zones", 2, rg2)
}

// TestReplicationCollectUnknownShapeWarns pins the wiring between Collect and
// warnUnknownHalShape: deleting the call site should fail this test even
// though TestWarnUnknownHalShape covers the helper in isolation.
func TestReplicationCollectUnknownShapeWarns(t *testing.T) {
	hook := test.NewGlobal()
	defer hook.Reset()

	mc := mockClient(t)
	mc.Responses[pathReplicationGroups] = `{"_embedded":{"_links":{}}}`

	samples, err := Replication{}.Collect(context.Background(), mc)
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
	if entry.Data["path"] != pathReplicationGroups {
		t.Errorf("path field = %v, want %v", entry.Data["path"], pathReplicationGroups)
	}
}
