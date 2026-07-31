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
		// When Flux is enabled it owns the four per-node performance names the
		// dashboard payloads no longer carry on 4.3. Deciding here, once, keeps a
		// single answer to "who emits this name" and makes it true before any
		// request is issued.
		Nodes{FluxOwnsPerf: cl.CollectFlux},
		Info{},
	}
	if cl.MeteringEnabled() {
		rcs = append(rcs, Metering{})
		if cl.QuotasEnabled() {
			rcs = append(rcs, Quotas{})
		}
	}
	if cl.CollectDT {
		rcs = append(rcs, NewDT(cl))
	}
	if cl.CollectFlux {
		// The DT collector, where it runs, owns ecs_node_dt_total: it is the only
		// source of unready and unknown per node, and Flux has no breakdown of
		// either. Decided here, once, like Nodes' arbitration above.
		rcs = append(rcs, Flux{DTOwnedByDT: cl.CollectDT, silent: &silenceSet{}})
	}
	return rcs
}
