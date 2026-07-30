package ecs

import (
	"context"

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

// Metering collects per-namespace billing (usage) stats. Quota limits are the
// Quotas collector's job.
type Metering struct{}

// Name identifies this collector in ecs_collector_up.
func (Metering) Name() string { return "metering" }

// Collect lists namespaces and pulls usage for all of them in one bulk billing
// call — one POST regardless of namespace count.
func (Metering) Collect(ctx context.Context, c ecsclient.Client) ([]Sample, error) {
	names, err := namespaceNames(ctx, c)
	if err != nil {
		return nil, err
	}

	var billing billingBulkResp
	if err := c.Post(ctx, pathBillingBulk, billingBulkReq{ID: names}, &billing); err != nil {
		return nil, err
	}
	var out []Sample
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

// namespaceNames lists the cluster's namespaces, dropping unnamed entries. Both
// namespace collectors need it, and neither can cache it for the other:
// collectors are independent by design (ADR-0009), so each pays one listing.
func namespaceNames(ctx context.Context, c ecsclient.Client) ([]string, error) {
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
	return names, nil
}
