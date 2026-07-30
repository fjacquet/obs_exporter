package ecs

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// quotaConcurrency caps the in-flight per-namespace quota GETs. The management
// API has no bulk quota endpoint, so this is one request per namespace per
// cycle; a limit keeps a thousand-namespace cluster from opening a thousand
// connections at once while still finishing well inside a collection interval.
// It is derived from the client's idle-connection pool size rather than chosen
// independently: fanning out wider than the pool trades pooled connections for
// a TLS handshake per excess request, every cycle.
const quotaConcurrency = ecsclient.MaxConcurrentRequests

// namespaceQuotaResp models GET /object/namespaces/namespace/{ns}/quota.
// blockSize (hard quota) and notificationSize (soft notification threshold)
// are in GiB; -1 means unset.
type namespaceQuotaResp struct {
	Namespace        string `json:"namespace"`
	BlockSize        Num    `json:"blockSize"`
	NotificationSize Num    `json:"notificationSize"`
}

// Quotas collects per-namespace quota limits.
//
// It is a collector of its own rather than a flag inside Metering so that
// operators get ecs_collector_up{collector="quotas"}: quotas are the only part
// of a cycle that scales with namespace count, and a cluster whose quota reads
// all fail on permissions would otherwise be indistinguishable from one with
// collectQuotas turned off — both produce no quota samples behind a metering
// collector still reporting up=1.
//
// The cost of that separation is one extra namespace listing per cycle, and the
// loss of overlap with Metering's billing POST: collectors run in sequence
// within a cluster (ADR-0009). Both are small against the N quota GETs this
// guards, and both would be recovered by running a cluster's collectors
// concurrently.
type Quotas struct{}

// Name identifies this collector in ecs_collector_up.
func (Quotas) Name() string { return "quotas" }

// Collect lists namespaces and fetches every quota concurrently.
//
// A quota that cannot be read is logged and skipped rather than failing the
// collector: one namespace's permissions must not blank the other thousand.
// Only the namespace listing failing takes the collector down, since without it
// there is nothing to report at all.
func (Quotas) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	names, err := namespaceNames(ctx, c)
	if err != nil {
		return nil, err
	}

	// Results go to a per-namespace slot rather than a shared append, so the
	// emitted order stays the inventory order regardless of completion order.
	per := make([][]Sample, len(names))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(quotaConcurrency)
	for i, name := range names {
		g.Go(func() error {
			var q namespaceQuotaResp
			path := fmt.Sprintf("%s/namespace/%s/quota", pathNamespaces, url.PathEscape(name))
			if err := c.Get(gctx, path, &q); err != nil {
				log.WithFields(log.Fields{"cluster": c.Name(), "namespace": name, "err": err}).
					Debug("namespace quota fetch failed")
				return nil
			}
			// This goroutine owns slot i, so appending straight into it is
			// race-free. Sized for the two samples a namespace can produce.
			nsLabel := []Label{{Key: "namespace", Value: name}}
			per[i] = make([]Sample, 0, 2)
			if q.BlockSize.Set && q.BlockSize.Val >= 0 {
				per[i] = append(per[i], Sample{Name: "ecs_namespace_quota_hard_bytes", Labels: nsLabel, Value: q.BlockSize.Val * gib})
			}
			if q.NotificationSize.Set && q.NotificationSize.Val >= 0 {
				per[i] = append(per[i], Sample{Name: "ecs_namespace_quota_soft_bytes", Labels: nsLabel, Value: q.NotificationSize.Val * gib})
			}
			return nil
		})
	}
	_ = g.Wait() // every goroutine degrades gracefully and returns nil
	return slices.Concat(per...), nil
}
