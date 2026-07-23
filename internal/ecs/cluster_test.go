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

	mustSample(t, samples, "ecs_cluster_nodes", 4)
	mustSample(t, samples, "ecs_cluster_good_nodes", 4)
	mustSample(t, samples, "ecs_cluster_bad_nodes", 0)
	mustSample(t, samples, "ecs_cluster_maintenance_nodes", 0)
	mustSample(t, samples, "ecs_cluster_disks", 32)
	mustSample(t, samples, "ecs_cluster_good_disks", 31)
	mustSample(t, samples, "ecs_cluster_bad_disks", 1)
	mustSample(t, samples, "ecs_cluster_maintenance_disks", 0)
	mustSample(t, samples, "ecs_cluster_ready_to_replace_disks", 0)

	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 1, Label{"severity", "critical"})
	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 0, Label{"severity", "error"})
	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 3, Label{"severity", "info"})
	mustSample(t, samples, "ecs_cluster_alerts_unacknowledged", 2, Label{"severity", "warning"})

	// "Current" value must be the newest point of each series. Values are
	// internally coherent per the ECS model total = allocated + free + reserved
	// (offline is a separate dimension, not part of the online total):
	// 12000 = 5000 + 5500 + 1500.
	mustSample(t, samples, "ecs_cluster_disk_space_total_bytes", 12000)
	mustSample(t, samples, "ecs_cluster_disk_space_free_bytes", 5500)
	mustSample(t, samples, "ecs_cluster_disk_space_allocated_bytes", 5000)
	mustSample(t, samples, "ecs_cluster_disk_space_reserved_bytes", 1500)
	mustSample(t, samples, "ecs_cluster_disk_space_offline_total_bytes", 300)

	mustSample(t, samples, "ecs_cluster_transaction_read_latency_milliseconds", 12)
	mustSample(t, samples, "ecs_cluster_transaction_write_latency_milliseconds", 22)
	mustSample(t, samples, "ecs_cluster_transaction_read_bandwidth_mb_per_second", 110)
	mustSample(t, samples, "ecs_cluster_transaction_write_bandwidth_mb_per_second", 220)
	mustSample(t, samples, "ecs_cluster_transactions_read_per_second", 1100)
	mustSample(t, samples, "ecs_cluster_transactions_write_per_second", 2200)

	mustSample(t, samples, "ecs_cluster_transaction_errors_total", 6298)
	mustSample(t, samples, "ecs_cluster_transaction_successes_total", 2020)
	mustSample(t, samples, "ecs_cluster_transaction_errors", 6293,
		Label{"code", "404"}, Label{"protocol", "S3"}, Label{"category", "User"})
	mustSample(t, samples, "ecs_cluster_transaction_errors", 1,
		Label{"code", "412"}, Label{"protocol", "ATMOS"}, Label{"category", "User"})

	mustSample(t, samples, "ecs_cluster_replication_ingress_traffic", 50000)
	mustSample(t, samples, "ecs_cluster_replication_egress_traffic", 35000)

	mustSample(t, samples, "ecs_cluster_replication_rpo_lag_seconds", 7200)
	mustSample(t, samples, "ecs_cluster_replication_rpo_timestamp_seconds", 1502820000)
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
