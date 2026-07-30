package ecs

// allocationComponentFields carries the breakdown of allocated space by what
// holds it.
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
//
// The label key is "type", the one key this exporter uses for "which partition
// of a byte quantity" — the same key ecs_cluster_disk_space_bytes and
// ecs_replication_group_chunks_pending_bytes carry, so one `by (type)` clause
// works across all three families (ADR-0012).
func (a allocationComponentFields) samples() []Sample {
	const name = "ecs_cluster_disk_space_allocated_component_bytes"

	var out []Sample
	out = appendSeries(out, name, a.UserData, Label{Key: "type", Value: "user_data"})
	out = appendSeries(out, name, a.SystemMetadata, Label{Key: "type", Value: "system_metadata"})
	out = appendSeries(out, name, a.GeoCache, Label{Key: "type", Value: "geo_cache"})
	out = appendSeries(out, name, a.GeoCopy, Label{Key: "type", Value: "geo_copy"})
	out = appendSeries(out, name, a.LocalProtection, Label{Key: "type", Value: "local_protection"})
	return out
}
