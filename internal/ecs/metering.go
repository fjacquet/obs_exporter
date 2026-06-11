package ecs

import (
	"context"
	"fmt"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
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

// Metering collects per-namespace quota and billing (usage) stats.
type Metering struct{}

// Name identifies this collector in ecs_collector_up.
func (Metering) Name() string { return "metering" }

// Collect lists namespaces, fetches each quota, and pulls usage for all
// namespaces in one bulk billing call.
func (Metering) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
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
	for _, name := range names {
		var q namespaceQuotaResp
		if err := c.Get(ctx, fmt.Sprintf("%s/namespace/%s/quota", pathNamespaces, name), &q); err != nil {
			// One namespace's quota failure shouldn't drop the whole domain.
			continue
		}
		nsLabel := []Label{{Key: "namespace", Value: name}}
		if q.BlockSize.Set && q.BlockSize.Val >= 0 {
			out = append(out, Sample{Name: "ecs_namespace_quota_hard_bytes", Labels: nsLabel, Value: q.BlockSize.Val * gib})
		}
		if q.NotificationSize.Set && q.NotificationSize.Val >= 0 {
			out = append(out, Sample{Name: "ecs_namespace_quota_soft_bytes", Labels: nsLabel, Value: q.NotificationSize.Val * gib})
		}
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
