package ecs

import (
	"context"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

// ResourceCollector collects one metric domain from a single ECS cluster. It
// returns cluster-agnostic samples; the loop stamps the `cluster` label.
// Implementations own their endpoint path and JSON structs so an API change is
// localized to one file.
type ResourceCollector interface {
	Name() string
	Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error)
}

// Registry returns the ordered set of collectors to run for one cluster,
// honoring its per-cluster feature flags.
func Registry(cl config.Cluster) []ResourceCollector {
	rcs := []ResourceCollector{
		Cluster{},
		Replication{},
		Nodes{},
		Info{},
	}
	if cl.MeteringEnabled() {
		rcs = append(rcs, Metering{})
	}
	if cl.CollectDT {
		rcs = append(rcs, NewDT(cl))
	}
	return rcs
}
