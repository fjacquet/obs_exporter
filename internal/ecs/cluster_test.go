package ecs

import (
	"context"
	"testing"
)

func TestClusterCollect(t *testing.T) {
	samples, err := Cluster{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	mustSample(t, samples, "ecs_cluster_nodes_installed", 4)
	mustSample(t, samples, "ecs_cluster_nodes", 4, Label{"state", "good"})
	mustSample(t, samples, "ecs_cluster_nodes", 0, Label{"state", "bad"})
	mustSample(t, samples, "ecs_cluster_nodes", 0, Label{"state", "maintenance"})
	mustSample(t, samples, "ecs_cluster_disks_installed", 32)
	mustSample(t, samples, "ecs_cluster_disks", 31, Label{"state", "good"})
	mustSample(t, samples, "ecs_cluster_disks", 1, Label{"state", "bad"})
	mustSample(t, samples, "ecs_cluster_disks", 0, Label{"state", "maintenance"})
	mustSample(t, samples, "ecs_cluster_disks", 0, Label{"state", "ready_to_replace"})

	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 1, Label{"severity", "critical"})
	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 0, Label{"severity", "error"})
	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 3, Label{"severity", "info"})
	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 2, Label{"severity", "warning"})

	// "Current" value must be the newest point of each series. Values are
	// internally coherent per the ECS model total = allocated + free + reserved
	// (offline is a separate dimension, not part of the online total):
	// 12000 = 5000 + 5500 + 1500.
	mustSample(t, samples, "ecs_cluster_disk_space_total_bytes", 12000)
	mustSample(t, samples, "ecs_cluster_disk_space_bytes", 5500, Label{"type", "free"})
	mustSample(t, samples, "ecs_cluster_disk_space_bytes", 5000, Label{"type", "allocated"})
	mustSample(t, samples, "ecs_cluster_disk_space_bytes", 1500, Label{"type", "reserved"})
	mustSample(t, samples, "ecs_cluster_disk_space_offline_total_bytes", 300)

	mustSample(t, samples, "ecs_cluster_transaction_latency_milliseconds", 12, Label{"op", "read"})
	mustSample(t, samples, "ecs_cluster_transaction_latency_milliseconds", 22, Label{"op", "write"})
	mustSample(t, samples, "ecs_cluster_transaction_bandwidth_mb_per_second", 110, Label{"op", "read"})
	mustSample(t, samples, "ecs_cluster_transaction_bandwidth_mb_per_second", 220, Label{"op", "write"})
	mustSample(t, samples, "ecs_cluster_transactions_per_second", 1100, Label{"op", "read"})
	mustSample(t, samples, "ecs_cluster_transactions_per_second", 2200, Label{"op", "write"})

	mustSample(t, samples, "ecs_cluster_transactions_total", 6298, Label{"outcome", "error"})
	mustSample(t, samples, "ecs_cluster_transactions_total", 2020, Label{"outcome", "success"})
	mustSample(t, samples, "ecs_cluster_transaction_errors", 6293,
		Label{"code", "404"}, Label{"protocol", "S3"}, Label{"category", "User"})
	mustSample(t, samples, "ecs_cluster_transaction_errors", 1,
		Label{"code", "412"}, Label{"protocol", "ATMOS"}, Label{"category", "User"})

	mustSample(t, samples, "ecs_cluster_replication_traffic", 50000, Label{"direction", "ingress"})
	mustSample(t, samples, "ecs_cluster_replication_traffic", 35000, Label{"direction", "egress"})

	mustSample(t, samples, "ecs_cluster_replication_rpo_lag_seconds", 7200)
	mustSample(t, samples, "ecs_cluster_replication_rpo_timestamp_seconds", 1502820000)

	gcUser := Label{"scope", "user"}
	gcSystem := Label{"scope", "system"}
	mustSample(t, samples, "ecs_cluster_gc_bytes", 900, gcUser, Label{"state", "pending"})
	mustSample(t, samples, "ecs_cluster_gc_reclaimed_bytes_total", 8100, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_bytes", 640, gcUser, Label{"state", "unreclaimable"})
	mustSample(t, samples, "ecs_cluster_gc_detected_bytes_total", 9700, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_enabled", 1, gcUser)
	mustSample(t, samples, "ecs_cluster_gc_bytes", 130, gcSystem, Label{"state", "pending"})
	// The fixture omits gcSystemMetadataIsEnabled on purpose.
	if _, ok := findSample(samples, "ecs_cluster_gc_enabled", gcSystem); ok {
		t.Error("gc_enabled{scope=system} should be absent: the fixture omits the flag")
	}

	mustSample(t, samples, "ecs_cluster_recovery_bad_chunks_bytes", 10992)
	mustSample(t, samples, "ecs_cluster_recovery_complete_time_estimate", 45.5)
	// The fixture sets recoveryRateCurrent to "N/A" on purpose.
	if _, ok := findSample(samples, "ecs_cluster_recovery_rate"); ok {
		t.Error("recovery_rate should be absent: the fixture value is unparseable")
	}

	mustSample(t, samples, "ecs_cluster_ec_applicable_bytes", 59000)
	mustSample(t, samples, "ecs_cluster_ec_coded_bytes", 58000)
	mustSample(t, samples, "ecs_cluster_ec_coded_ratio_percent", 98.3)
	mustSample(t, samples, "ecs_cluster_ec_rate", 12.5)
	mustSample(t, samples, "ecs_cluster_ec_complete_time_estimate", 3.25)

	mustSample(t, samples, "ecs_cluster_disk_space_allocated_component_bytes", 3100, Label{"purpose", "user_data"})
	mustSample(t, samples, "ecs_cluster_disk_space_allocated_component_bytes", 1200, Label{"purpose", "system_metadata"})
	mustSample(t, samples, "ecs_cluster_disk_space_allocated_component_bytes", 600, Label{"purpose", "local_protection"})
	// The fixture omits the geo components on purpose: the breakdown is not
	// exhaustive, and 3100+1200+600 = 4900 against an allocated total of 5000.
	if _, ok := findSample(samples, "ecs_cluster_disk_space_allocated_component_bytes", Label{"purpose", "geo_cache"}); ok {
		t.Error("purpose=geo_cache should be absent: the fixture omits it")
	}
}

func TestSplitErrorType(t *testing.T) {
	cases := []struct{ in, code, proto string }{
		{"403 (S3)", "403", "S3"},
		{"412 (ATMOS)", "412", "ATMOS"},
		{"weird", "weird", ""},
	}
	for _, c := range cases {
		code, proto := splitErrorType(c.in)
		if code != c.code || proto != c.proto {
			t.Errorf("splitErrorType(%q) = (%q, %q), want (%q, %q)", c.in, code, proto, c.code, c.proto)
		}
	}
}
