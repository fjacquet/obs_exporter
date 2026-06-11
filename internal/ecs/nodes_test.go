package ecs

import (
	"context"
	"testing"
)

func TestNodesCollect(t *testing.T) {
	samples, err := Nodes{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}

	n1 := Label{"node", "supr01-r01"}
	mustSample(t, samples, "ecs_node_healthy", 1, n1)
	mustSample(t, samples, "ecs_node_disks", 40, n1)
	mustSample(t, samples, "ecs_node_good_disks", 40, n1)
	mustSample(t, samples, "ecs_node_disk_space_total_bytes", 510, n1)
	mustSample(t, samples, "ecs_node_disk_space_free_bytes", 90, n1)
	mustSample(t, samples, "ecs_node_disk_space_allocated_bytes", 420, n1)
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 43, n1)
	mustSample(t, samples, "ecs_node_memory_utilization_percent", 35, n1)
	mustSample(t, samples, "ecs_node_memory_used_bytes", 11000, n1)
	mustSample(t, samples, "ecs_node_nic_received_bandwidth", 4300, n1)
	mustSample(t, samples, "ecs_node_nic_transmitted_bandwidth", 3009, n1)
	mustSample(t, samples, "ecs_node_nic_utilization_percent", 14, n1)
	mustSample(t, samples, "ecs_node_transaction_read_latency_milliseconds", 9, n1)
	mustSample(t, samples, "ecs_node_transactions_write_per_second", 1600, n1)

	n2 := Label{"node", "supr01-r02"}
	mustSample(t, samples, "ecs_node_healthy", 0, n2)
	mustSample(t, samples, "ecs_node_bad_disks", 1, n2)
	mustSample(t, samples, "ecs_node_ready_to_replace_disks", 1, n2)
	mustSample(t, samples, "ecs_node_cpu_utilization_percent", 88, n2)
	// node 2 reports no NIC stats: samples must be absent, not zero.
	if _, ok := findSample(samples, "ecs_node_nic_utilization_percent", n2); ok {
		t.Error("nic utilization for node2 should be absent")
	}
}

func TestInfoCollect(t *testing.T) {
	samples, err := Info{}.Collect(context.Background(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	mustSample(t, samples, "ecs_cluster_info", 1, Label{"version", "4.1.0.0.12345"})
}
