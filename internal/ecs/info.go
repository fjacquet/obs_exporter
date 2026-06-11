package ecs

import (
	"context"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

const pathVdcNodes = "/vdc/nodes"

// vdcNodesResp models GET /vdc/nodes — the management node inventory. Only the
// software version (and, for the DT collector, the per-node management IPs) are
// consumed from it.
type vdcNodesResp struct {
	Node []struct {
		Version string `json:"version"`
		MgmtIP  string `json:"mgmt_ip"`
		DataIP  string `json:"data_ip"`
	} `json:"node"`
}

// Info emits the constant-value cluster identity metric carrying the ECS
// software version.
type Info struct{}

// Name identifies this collector in ecs_collector_up.
func (Info) Name() string { return "info" }

// Collect fetches /vdc/nodes and emits ecs_cluster_info{version=...} = 1.
func (Info) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var r vdcNodesResp
	if err := c.Get(ctx, pathVdcNodes, &r); err != nil {
		return nil, err
	}
	version := ""
	if len(r.Node) > 0 {
		version = r.Node[0].Version
	}
	return []Sample{{
		Name:   "ecs_cluster_info",
		Labels: []Label{{Key: "version", Value: version}},
		Value:  1,
	}}, nil
}
