package ecs

import (
	"context"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

const pathReplicationGroups = "/dashboard/zones/localzone/replicationgroups"

// replicationGroupsResp models GET /dashboard/zones/localzone/replicationgroups
// (OBS 4.1): a HAL-style list of per-replication-group instances. See halList
// for why both spellings of the array key are accepted.
type replicationGroupsResp struct {
	Embedded halList[replicationGroupInstance] `json:"_embedded"`
}

// replicationGroupInstance is one per-group entry of the dashboard payload.
type replicationGroupInstance struct {
	Name                                     string `json:"name"`
	NumZones                                 Num    `json:"numZones"`
	ReplicationIngressTraffic                Series `json:"replicationIngressTraffic"`
	ReplicationEgressTraffic                 Series `json:"replicationEgressTraffic"`
	ChunksRepoPendingReplicationTotalSize    Num    `json:"chunksRepoPendingReplicationTotalSize"`
	ChunksJournalPendingReplicationTotalSize Num    `json:"chunksJournalPendingReplicationTotalSize"`
	ChunksPendingXorTotalSize                Num    `json:"chunksPendingXorTotalSize"`
	ReplicationRpoTimestamp                  Num    `json:"replicationRpoTimestamp"`
	ReplicationRpoLag                        Num    `json:"replicationRpoLag"`
}

// Replication collects per-replication-group traffic, backlog, and RPO stats.
type Replication struct{}

// Name identifies this collector in ecs_collector_up.
func (Replication) Name() string { return "replication" }

// Collect fetches the replication-group dashboard and maps it to samples.
func (Replication) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var r replicationGroupsResp
	if err := c.Get(ctx, pathReplicationGroups, &r); err != nil {
		return nil, err
	}
	warnHalShape(c.Name(), pathReplicationGroups, r.Embedded.halShape)
	var out []Sample
	for _, rg := range r.Embedded.Instances {
		group := Label{Key: "rg", Value: rg.Name}
		// The traffic pair mirrors the cluster-level one, and the three chunk
		// backlogs are disjoint pools of the same measure in the same unit, so both
		// collapse to one name plus a dimension (ADR-0012).
		out = appendSeries(out, "ecs_replication_group_traffic", rg.ReplicationIngressTraffic, group, Label{"direction", "ingress"})
		out = appendSeries(out, "ecs_replication_group_traffic", rg.ReplicationEgressTraffic, group, Label{"direction", "egress"})
		out = appendNum(out, "ecs_replication_group_chunks_pending_bytes", rg.ChunksRepoPendingReplicationTotalSize, group, Label{"type", "repo"})
		out = appendNum(out, "ecs_replication_group_chunks_pending_bytes", rg.ChunksJournalPendingReplicationTotalSize, group, Label{"type", "journal"})
		out = appendNum(out, "ecs_replication_group_chunks_pending_bytes", rg.ChunksPendingXorTotalSize, group, Label{"type", "xor"})
		out = appendNum(out, "ecs_replication_group_rpo_timestamp_seconds", rg.ReplicationRpoTimestamp, group)
		out = appendNum(out, "ecs_replication_group_rpo_lag_seconds", rg.ReplicationRpoLag, group)
		out = appendNum(out, "ecs_replication_group_zones", rg.NumZones, group)
	}
	return out, nil
}
