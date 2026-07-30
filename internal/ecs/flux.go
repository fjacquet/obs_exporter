package ecs

import (
	"context"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
)

// Flux is the opt-in collector for metric families the management API does
// not serve, sourced from the cluster's Flux/InfluxDB monitoring store. The
// query table and emission logic arrive in the following task; registering
// the collector here — even inert — is what makes Registry's arbitration
// decision (which source owns the three contested per-node names) testable
// now, before either half of the query/emission work exists.
type Flux struct{}

// Name identifies this collector in ecs_collector_up.
func (Flux) Name() string { return "flux" }

// Collect is a stub: it issues no requests and returns no samples. Replaced
// in the next task with the Flux query table and Sample emission.
func (Flux) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	return nil, nil
}
