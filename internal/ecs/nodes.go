package ecs

import (
	"context"
	"slices"
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

	diskCountFields

	DiskSpaceTotal     Series `json:"diskSpaceTotal"`
	DiskSpaceFree      Series `json:"diskSpaceFree"`
	DiskSpaceAllocated Series `json:"diskSpaceAllocated"`

	NodeCPUUtilization         Series `json:"nodeCpuUtilization"`
	NodeMemoryUtilization      Series `json:"nodeMemoryUtilization"`
	NodeMemoryUtilizationBytes Series `json:"nodeMemoryUtilizationBytes"`

	NodeNicReceivedBandwidth    Series `json:"nodeNicReceivedBandwidth"`
	NodeNicTransmittedBandwidth Series `json:"nodeNicTransmittedBandwidth"`
	NodeNicUtilization          Series `json:"nodeNicUtilization"`

	transactionFields
}

// Nodes collects per-node health, capacity, utilization, and transaction stats
// from the dashboard API. FluxOwnsPerf suppresses the four names the Flux
// collector takes over, so exactly one source emits each per cycle (ADR-0006).
type Nodes struct{ FluxOwnsPerf bool }

// Name identifies this collector in ecs_collector_up.
func (Nodes) Name() string { return "nodes" }

// Collect fetches the per-node dashboard list and maps it to samples.
func (nc Nodes) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var r localZoneNodesResp
	if err := c.Get(ctx, pathLocalZoneNodes, &r); err != nil {
		return nil, err
	}
	warnHalShape(c.Name(), pathLocalZoneNodes, r.Embedded.halShape)
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

		out = append(out, n.diskCountFields.samples("ecs_node", node)...)

		// Unlike the cluster payload, the per-node one publishes no reserved
		// series, so allocated + free falls short of the total by the reserve (10%
		// of total on a live 4.3 cluster). The total therefore keeps its own name
		// AND the labeled family is knowingly incomplete — documented in
		// docs/metrics/index.md so nobody reads the difference as free space.
		out = appendSeries(out, "ecs_node_disk_space_total_bytes", n.DiskSpaceTotal, node)
		out = appendSeries(out, "ecs_node_disk_space_bytes", n.DiskSpaceAllocated, node, Label{"type", "allocated"})
		out = appendSeries(out, "ecs_node_disk_space_bytes", n.DiskSpaceFree, node, Label{"type", "free"})

		if !nc.FluxOwnsPerf {
			out = appendSeries(out, "ecs_node_cpu_utilization_percent", n.NodeCPUUtilization, node)
			out = appendSeries(out, "ecs_node_memory_utilization_percent", n.NodeMemoryUtilization, node)
			out = appendSeries(out, "ecs_node_memory_used_bytes", n.NodeMemoryUtilizationBytes, node)
		}

		out = appendSeries(out, "ecs_node_nic_bandwidth", n.NodeNicReceivedBandwidth, node, Label{"direction", "received"})
		out = appendSeries(out, "ecs_node_nic_bandwidth", n.NodeNicTransmittedBandwidth, node, Label{"direction", "transmitted"})
		out = appendSeries(out, "ecs_node_nic_utilization_percent", n.NodeNicUtilization, node)

		tx := n.transactionFields.samples("ecs_node", node)
		if nc.FluxOwnsPerf {
			// Flux serves this family as a histogram, and Prometheus reads
			// ecs_node_transaction_latency_milliseconds_bucket as belonging to a
			// histogram of that name — so the gauge and the histogram cannot both
			// hold it (ADR-0006). The bandwidth and TPS names have no Flux
			// equivalent and stay here.
			tx = slices.DeleteFunc(tx, func(s Sample) bool {
				return s.Name == "ecs_node_transaction_latency_milliseconds"
			})
		}
		out = append(out, tx...)
	}
	return out, nil
}
