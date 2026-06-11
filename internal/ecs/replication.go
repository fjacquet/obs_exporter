package ecs

import (
	"context"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

const pathReplicationGroups = "/dashboard/zones/localzone/replicationgroups"

// replicationGroupsResp models GET /dashboard/zones/localzone/replicationgroups
// (OBS 4.1): a HAL-style list of per-replication-group instances.
type replicationGroupsResp struct {
	Embedded struct {
		Instances []struct {
			Name                                     string `json:"name"`
			NumZones                                 Num    `json:"numZones"`
			ReplicationIngressTraffic                Series `json:"replicationIngressTraffic"`
			ReplicationEgressTraffic                 Series `json:"replicationEgressTraffic"`
			ChunksRepoPendingReplicationTotalSize    Num    `json:"chunksRepoPendingReplicationTotalSize"`
			ChunksJournalPendingReplicationTotalSize Num    `json:"chunksJournalPendingReplicationTotalSize"`
			ChunksPendingXorTotalSize                Num    `json:"chunksPendingXorTotalSize"`
			ReplicationRpoTimestamp                  Num    `json:"replicationRpoTimestamp"`
			ReplicationRpoLag                        Num    `json:"replicationRpoLag"`
		} `json:"instances"`
	} `json:"_embedded"`
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
	var out []Sample
	for _, rg := range r.Embedded.Instances {
		rgLabel := []Label{{Key: "rg", Value: rg.Name}}
		num := func(name string, n Num) {
			if n.Set {
				out = append(out, Sample{Name: name, Labels: rgLabel, Value: n.Val})
			}
		}
		if v, ok := rg.ReplicationIngressTraffic.Latest(); ok {
			out = append(out, Sample{Name: "ecs_replication_group_ingress_traffic", Labels: rgLabel, Value: v})
		}
		if v, ok := rg.ReplicationEgressTraffic.Latest(); ok {
			out = append(out, Sample{Name: "ecs_replication_group_egress_traffic", Labels: rgLabel, Value: v})
		}
		num("ecs_replication_group_chunks_repo_pending_replication_bytes", rg.ChunksRepoPendingReplicationTotalSize)
		num("ecs_replication_group_chunks_journal_pending_replication_bytes", rg.ChunksJournalPendingReplicationTotalSize)
		num("ecs_replication_group_chunks_pending_xor_bytes", rg.ChunksPendingXorTotalSize)
		num("ecs_replication_group_rpo_timestamp_seconds", rg.ReplicationRpoTimestamp)
		num("ecs_replication_group_rpo_lag_seconds", rg.ReplicationRpoLag)
		num("ecs_replication_group_zones", rg.NumZones)
	}
	return out, nil
}
