package ecs

import (
	"context"
	"testing"
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
