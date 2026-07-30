package ecs

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	pathNamespaces = "/object/namespaces"
	// Bulk billing endpoint (OBS 4.1): one POST returns billing info for every
	// namespace in the request body, replacing the v1 exporter's per-namespace GETs.
	pathBillingBulk = "/object/billing/namespace/info?sizeunit=KB"

	gib = 1024 * 1024 * 1024
	kib = 1024
)

type namespacesResp struct {
	Namespace []struct {
		Name string `json:"name"`
	} `json:"namespace"`
}

// namespaceQuotaResp models GET /object/namespaces/namespace/{ns}/quota.
// blockSize (hard quota) and notificationSize (soft notification threshold)
// are in GiB; -1 means unset.
type namespaceQuotaResp struct {
	Namespace        string `json:"namespace"`
	BlockSize        Num    `json:"blockSize"`
	NotificationSize Num    `json:"notificationSize"`
}

type billingBulkReq struct {
	ID []string `json:"id"`
}

type billingBulkResp struct {
	Infos []struct {
		Namespace     string `json:"namespace"`
		TotalSize     Num    `json:"total_size"` // in KB (sizeunit=KB)
		TotalObjects  Num    `json:"total_objects"`
		TotalMpuSize  Num    `json:"total_mpu_size"` // in KB
		TotalMpuParts Num    `json:"total_mpu_parts"`
	} `json:"namespace_billing_infos"`
}

// quotaConcurrency caps the in-flight per-namespace quota GETs. The management
// API has no bulk quota endpoint, so this is one request per namespace per
// cycle; a limit keeps a thousand-namespace cluster from opening a thousand
// connections at once while still finishing well inside a collection interval.
const quotaConcurrency = 8

// Metering collects per-namespace quota and billing (usage) stats.
type Metering struct {
	// Quotas enables the per-namespace quota GETs. Billing is one bulk POST and
	// is always collected; quotas are the part that scales with namespace count.
	Quotas bool
}

// NewMetering builds the metering collector for one cluster's feature flags.
func NewMetering(cl config.Cluster) Metering {
	return Metering{Quotas: cl.QuotasEnabled()}
}

// Name identifies this collector in ecs_collector_up.
func (Metering) Name() string { return "metering" }

// Collect lists namespaces, fetches each quota, and pulls usage for all
// namespaces in one bulk billing call.
func (m Metering) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	var nss namespacesResp
	if err := c.Get(ctx, pathNamespaces, &nss); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nss.Namespace))
	for _, ns := range nss.Namespace {
		if ns.Name != "" {
			names = append(names, ns.Name)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}

	var out []Sample
	if m.Quotas {
		out = collectQuotas(ctx, c, names)
	}

	var billing billingBulkResp
	if err := c.Post(ctx, pathBillingBulk, billingBulkReq{ID: names}, &billing); err != nil {
		return out, err
	}
	for _, info := range billing.Infos {
		nsLabel := []Label{{Key: "namespace", Value: info.Namespace}}
		if info.TotalSize.Set {
			out = append(out, Sample{Name: "ecs_namespace_used_bytes", Labels: nsLabel, Value: info.TotalSize.Val * kib})
		}
		if info.TotalObjects.Set {
			out = append(out, Sample{Name: "ecs_namespace_objects", Labels: nsLabel, Value: info.TotalObjects.Val})
		}
		if info.TotalMpuSize.Set {
			out = append(out, Sample{Name: "ecs_namespace_mpu_used_bytes", Labels: nsLabel, Value: info.TotalMpuSize.Val * kib})
		}
		if info.TotalMpuParts.Set {
			out = append(out, Sample{Name: "ecs_namespace_mpu_parts", Labels: nsLabel, Value: info.TotalMpuParts.Val})
		}
	}
	return out, nil
}

// collectQuotas fetches every namespace's quota concurrently. Results are
// written to a per-namespace slot rather than appended, so the emitted order
// stays the inventory order regardless of which request finishes first.
func collectQuotas(ctx context.Context, c ecsclient.Client, names []string) []Sample {
	per := make([][]Sample, len(names))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(quotaConcurrency)
	for i, name := range names {
		g.Go(func() error {
			var q namespaceQuotaResp
			path := fmt.Sprintf("%s/namespace/%s/quota", pathNamespaces, url.PathEscape(name))
			if err := c.Get(gctx, path, &q); err != nil {
				// One namespace's quota failure shouldn't drop the whole domain.
				log.WithFields(log.Fields{"cluster": c.Name(), "namespace": name, "err": err}).
					Debug("namespace quota fetch failed")
				return nil
			}
			nsLabel := []Label{{Key: "namespace", Value: name}}
			var samples []Sample
			if q.BlockSize.Set && q.BlockSize.Val >= 0 {
				samples = append(samples, Sample{Name: "ecs_namespace_quota_hard_bytes", Labels: nsLabel, Value: q.BlockSize.Val * gib})
			}
			if q.NotificationSize.Set && q.NotificationSize.Val >= 0 {
				samples = append(samples, Sample{Name: "ecs_namespace_quota_soft_bytes", Labels: nsLabel, Value: q.NotificationSize.Val * gib})
			}
			per[i] = samples
			return nil
		})
	}
	_ = g.Wait() // every goroutine degrades gracefully and returns nil
	return slices.Concat(per...)
}
