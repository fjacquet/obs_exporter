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
	warnHalShape(c.Name(), pathLocalZoneNodes, r.Embedded.Shape())
	var out []Sample
	for _, n := range r.Embedded.Instances {
		node := Label{Key: "node", Value: n.DisplayName}
		healthy := 0.0
		if strings.EqualFold(n.HealthStatus, "good") {
			healthy = 1
		}
		out = append(out, Sample{Name: "ecs_node_healthy", Labels: []Label{node}, Value: healthy})

		// Enum/state pattern: expose the raw health state as a label so bad and
		// maintenance stay distinguishable (the boolean above collapses them).
		// Only the current state is emitted; the snapshot model drops stale
		// state series on the next cycle without manual bookkeeping.
		if n.HealthStatus != "" {
			out = append(out, Sample{
				Name:   "ecs_node_health_state",
				Labels: []Label{node, {Key: "state", Value: strings.ToLower(n.HealthStatus)}},
				Value:  1,
			})
		}

		// Same split as the cluster counts: the per-state counts share a name, the
		// installed total keeps its own because the states are not a proven
		// partition of it (ADR-0012).
		out = appendNum(out, "ecs_node_disks_installed", n.NumDisks, node)
		out = appendNum(out, "ecs_node_disks", n.NumGoodDisks, node, Label{"state", "good"})
		out = appendNum(out, "ecs_node_disks", n.NumBadDisks, node, Label{"state", "bad"})
		out = appendNum(out, "ecs_node_disks", n.NumMaintenanceDisks, node, Label{"state", "maintenance"})
		out = appendNum(out, "ecs_node_disks", n.NumReadyToReplaceDisks, node, Label{"state", "ready_to_replace"})

		// Unlike the cluster payload, the per-node one publishes no reserved
		// series, so allocated + free falls short of the total by the reserve (10%
		// of total on a live 4.3 cluster). The total therefore keeps its own name
		// AND the labeled family is knowingly incomplete — documented in
		// docs/metrics.md so nobody reads the difference as free space.
		out = appendSeries(out, "ecs_node_disk_space_total_bytes", n.DiskSpaceTotal, node)
		out = appendSeries(out, "ecs_node_disk_space_bytes", n.DiskSpaceAllocated, node, Label{"type", "allocated"})
		out = appendSeries(out, "ecs_node_disk_space_bytes", n.DiskSpaceFree, node, Label{"type", "free"})

		out = appendSeries(out, "ecs_node_cpu_utilization_percent", n.NodeCPUUtilization, node)
		out = appendSeries(out, "ecs_node_memory_utilization_percent", n.NodeMemoryUtilization, node)
		out = appendSeries(out, "ecs_node_memory_used_bytes", n.NodeMemoryUtilizationBytes, node)

		out = appendSeries(out, "ecs_node_nic_bandwidth", n.NodeNicReceivedBandwidth, node, Label{"direction", "received"})
		out = appendSeries(out, "ecs_node_nic_bandwidth", n.NodeNicTransmittedBandwidth, node, Label{"direction", "transmitted"})
		out = appendSeries(out, "ecs_node_nic_utilization_percent", n.NodeNicUtilization, node)

		read, write := Label{"op", "read"}, Label{"op", "write"}
		out = appendSeries(out, "ecs_node_transaction_latency_milliseconds", n.TransactionReadLatency, node, read)
		out = appendSeries(out, "ecs_node_transaction_latency_milliseconds", n.TransactionWriteLatency, node, write)
		out = appendSeries(out, "ecs_node_transaction_bandwidth_mb_per_second", n.TransactionReadBandwidth, node, read)
		out = appendSeries(out, "ecs_node_transaction_bandwidth_mb_per_second", n.TransactionWriteBandwidth, node, write)
		out = appendSeries(out, "ecs_node_transactions_per_second", n.TransactionReadTransactionsPerSec, node, read)
		out = appendSeries(out, "ecs_node_transactions_per_second", n.TransactionWriteTransactionsPerSec, node, write)
	}
	return out, nil
}
