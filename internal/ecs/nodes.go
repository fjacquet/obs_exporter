package ecs

import (
	"context"
	"strings"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

const pathLocalZoneNodes = "/dashboard/zones/localzone/nodes"

// localZoneNodesResp models GET /dashboard/zones/localzone/nodes (OBS 4.1): a
// HAL-style list of per-node dashboard instances. See halList for why both
// spellings of the array key are accepted.
type localZoneNodesResp struct {
	Embedded halList[nodeInstance] `json:"_embedded"`
}

// nodeInstance is one per-node entry of the local-zone dashboard payload.
type nodeInstance struct {
	DisplayName  string `json:"displayName"`
	HealthStatus string `json:"healthStatus"`

	NumDisks               Num `json:"numDisks"`
	NumGoodDisks           Num `json:"numGoodDisks"`
	NumBadDisks            Num `json:"numBadDisks"`
	NumMaintenanceDisks    Num `json:"numMaintenanceDisks"`
	NumReadyToReplaceDisks Num `json:"numReadyToReplaceDisks"`

	DiskSpaceTotal     Series `json:"diskSpaceTotal"`
	DiskSpaceFree      Series `json:"diskSpaceFree"`
	DiskSpaceAllocated Series `json:"diskSpaceAllocated"`

	NodeCPUUtilization         Series `json:"nodeCpuUtilization"`
	NodeMemoryUtilization      Series `json:"nodeMemoryUtilization"`
	NodeMemoryUtilizationBytes Series `json:"nodeMemoryUtilizationBytes"`

	NodeNicReceivedBandwidth    Series `json:"nodeNicReceivedBandwidth"`
	NodeNicTransmittedBandwidth Series `json:"nodeNicTransmittedBandwidth"`
	NodeNicUtilization          Series `json:"nodeNicUtilization"`

	TransactionReadLatency             Series `json:"transactionReadLatency"`
	TransactionWriteLatency            Series `json:"transactionWriteLatency"`
	TransactionReadBandwidth           Series `json:"transactionReadBandwidth"`
	TransactionWriteBandwidth          Series `json:"transactionWriteBandwidth"`
	TransactionReadTransactionsPerSec  Series `json:"transactionReadTransactionsPerSec"`
	TransactionWriteTransactionsPerSec Series `json:"transactionWriteTransactionsPerSec"`
}

// Nodes collects per-node health, capacity, utilization, and transaction stats
// from the documented dashboard nodes endpoint (replaces the v1 exporter's
// undocumented node-local DT scraping for general node metrics).
type Nodes struct{}

// Name identifies this collector in ecs_collector_up.
func (Nodes) Name() string { return "nodes" }

// Collect fetches the per-node dashboard list and maps it to samples.
func (Nodes) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var r localZoneNodesResp
	if err := c.Get(ctx, pathLocalZoneNodes, &r); err != nil {
		return nil, err
	}
	warnUnknownHalShape(c.Name(), pathLocalZoneNodes, r.Embedded.KeySeen)
	var out []Sample
	for _, n := range r.Embedded.Instances {
		nodeLabel := []Label{{Key: "node", Value: n.DisplayName}}
		healthy := 0.0
		if strings.EqualFold(n.HealthStatus, "good") {
			healthy = 1
		}
		out = append(out, Sample{Name: "ecs_node_healthy", Labels: nodeLabel, Value: healthy})

		// Enum/state pattern: expose the raw health state as a label so bad and
		// maintenance stay distinguishable (the boolean above collapses them).
		// Only the current state is emitted; the snapshot model drops stale
		// state series on the next cycle without manual bookkeeping.
		if n.HealthStatus != "" {
			out = append(out, Sample{
				Name: "ecs_node_health_state",
				Labels: []Label{
					{Key: "node", Value: n.DisplayName},
					{Key: "state", Value: strings.ToLower(n.HealthStatus)},
				},
				Value: 1,
			})
		}

		out = appendNum(out, "ecs_node_disks", n.NumDisks, nodeLabel...)
		out = appendNum(out, "ecs_node_good_disks", n.NumGoodDisks, nodeLabel...)
		out = appendNum(out, "ecs_node_bad_disks", n.NumBadDisks, nodeLabel...)
		out = appendNum(out, "ecs_node_maintenance_disks", n.NumMaintenanceDisks, nodeLabel...)
		out = appendNum(out, "ecs_node_ready_to_replace_disks", n.NumReadyToReplaceDisks, nodeLabel...)

		out = appendSeries(out, "ecs_node_disk_space_total_bytes", n.DiskSpaceTotal, nodeLabel...)
		out = appendSeries(out, "ecs_node_disk_space_free_bytes", n.DiskSpaceFree, nodeLabel...)
		out = appendSeries(out, "ecs_node_disk_space_allocated_bytes", n.DiskSpaceAllocated, nodeLabel...)

		out = appendSeries(out, "ecs_node_cpu_utilization_percent", n.NodeCPUUtilization, nodeLabel...)
		out = appendSeries(out, "ecs_node_memory_utilization_percent", n.NodeMemoryUtilization, nodeLabel...)
		out = appendSeries(out, "ecs_node_memory_used_bytes", n.NodeMemoryUtilizationBytes, nodeLabel...)

		out = appendSeries(out, "ecs_node_nic_received_bandwidth", n.NodeNicReceivedBandwidth, nodeLabel...)
		out = appendSeries(out, "ecs_node_nic_transmitted_bandwidth", n.NodeNicTransmittedBandwidth, nodeLabel...)
		out = appendSeries(out, "ecs_node_nic_utilization_percent", n.NodeNicUtilization, nodeLabel...)

		out = appendSeries(out, "ecs_node_transaction_read_latency_milliseconds", n.TransactionReadLatency, nodeLabel...)
		out = appendSeries(out, "ecs_node_transaction_write_latency_milliseconds", n.TransactionWriteLatency, nodeLabel...)
		out = appendSeries(out, "ecs_node_transaction_read_bandwidth_mb_per_second", n.TransactionReadBandwidth, nodeLabel...)
		out = appendSeries(out, "ecs_node_transaction_write_bandwidth_mb_per_second", n.TransactionWriteBandwidth, nodeLabel...)
		out = appendSeries(out, "ecs_node_transactions_read_per_second", n.TransactionReadTransactionsPerSec, nodeLabel...)
		out = appendSeries(out, "ecs_node_transactions_write_per_second", n.TransactionWriteTransactionsPerSec, nodeLabel...)
	}
	return out, nil
}
