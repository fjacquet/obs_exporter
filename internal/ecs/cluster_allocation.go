package ecs

// allocationComponentFields carries the breakdown of allocated space by purpose.
//
// This is deliberately a separate metric name rather than a label on the existing
// ecs_cluster_disk_space_allocated_bytes: adding a label to a published metric
// would break the ADR-0006 invariant and every existing query.
//
// The breakdown is NOT exhaustive. On a live ObjectScale 4.3 cluster these five
// components summed to 12.8% less than diskSpaceAllocatedCurrent. Never pad a
// missing component with zero — that would imply the parts account for the whole.
type allocationComponentFields struct {
	UserData        Series `json:"diskSpaceAllocatedUserDataCurrent"`
	SystemMetadata  Series `json:"diskSpaceAllocatedSystemMetadataCurrent"`
	GeoCache        Series `json:"diskSpaceAllocatedGeoCacheCurrent"`
	GeoCopy         Series `json:"diskSpaceAllocatedGeoCopyCurrent"`
	LocalProtection Series `json:"diskSpaceAllocatedLocalProtectionCurrent"`
}

// samples maps the allocation breakdown to cluster-agnostic samples. Missing or
// unparseable components yield absent samples, never zeros.
func (a allocationComponentFields) samples() []Sample {
	const name = "ecs_cluster_disk_space_allocated_component_bytes"

	var out []Sample
	out = appendSeries(out, name, a.UserData, Label{Key: "purpose", Value: "user_data"})
	out = appendSeries(out, name, a.SystemMetadata, Label{Key: "purpose", Value: "system_metadata"})
	out = appendSeries(out, name, a.GeoCache, Label{Key: "purpose", Value: "geo_cache"})
	out = appendSeries(out, name, a.GeoCopy, Label{Key: "purpose", Value: "geo_copy"})
	out = appendSeries(out, name, a.LocalProtection, Label{Key: "purpose", Value: "local_protection"})
	return out
}
