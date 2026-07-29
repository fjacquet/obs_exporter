package ecs

// gcFields carries the garbage-collection block of the local-zone dashboard.
//
// Watch the API's asymmetric naming: the numeric series are keyed User/System
// while the two enable flags are keyed UserData/SystemMetadata. Both map onto the
// same "scope" label value — this is the API's inconsistency, not a typo to fix.
//
// gcCombined* is deliberately not decoded: it equals user + system exactly
// (verified to the byte against a live ObjectScale 4.3 cluster), so exporting it
// would make sum() double-count. `sum without(scope)` reproduces it.
type gcFields struct {
	GCUserPending       Series `json:"gcUserPendingCurrent"`
	GCUserReclaimed     Series `json:"gcUserReclaimedCurrent"`
	GCUserUnreclaimable Series `json:"gcUserUnreclaimableCurrent"`
	GCUserTotalDetected Series `json:"gcUserTotalDetectedCurrent"`
	GCUserDataIsEnabled Bool   `json:"gcUserDataIsEnabled"`

	GCSystemPending           Series `json:"gcSystemPendingCurrent"`
	GCSystemReclaimed         Series `json:"gcSystemReclaimedCurrent"`
	GCSystemUnreclaimable     Series `json:"gcSystemUnreclaimableCurrent"`
	GCSystemTotalDetected     Series `json:"gcSystemTotalDetectedCurrent"`
	GCSystemMetadataIsEnabled Bool   `json:"gcSystemMetadataIsEnabled"`
}

// samples maps the GC block to cluster-agnostic samples. Missing or unparseable
// values yield absent samples, never zeros.
//
// Reclaimed and detected carry _total because they are lifetime counters, not
// backlogs: on a live 4.3 cluster detected equalled pending + unreclaimable +
// reclaimed exactly, on both scopes. Pending and unreclaimable are the two that
// can fall as well as rise, so they stay unsuffixed gauges.
func (g gcFields) samples() []Sample {
	var out []Sample

	user := Label{Key: "scope", Value: "user"}
	out = appendSeries(out, "ecs_cluster_gc_pending_bytes", g.GCUserPending, user)
	out = appendSeries(out, "ecs_cluster_gc_reclaimed_bytes_total", g.GCUserReclaimed, user)
	out = appendSeries(out, "ecs_cluster_gc_unreclaimable_bytes", g.GCUserUnreclaimable, user)
	out = appendSeries(out, "ecs_cluster_gc_detected_bytes_total", g.GCUserTotalDetected, user)
	out = appendBool(out, "ecs_cluster_gc_enabled", g.GCUserDataIsEnabled, user)

	system := Label{Key: "scope", Value: "system"}
	out = appendSeries(out, "ecs_cluster_gc_pending_bytes", g.GCSystemPending, system)
	out = appendSeries(out, "ecs_cluster_gc_reclaimed_bytes_total", g.GCSystemReclaimed, system)
	out = appendSeries(out, "ecs_cluster_gc_unreclaimable_bytes", g.GCSystemUnreclaimable, system)
	out = appendSeries(out, "ecs_cluster_gc_detected_bytes_total", g.GCSystemTotalDetected, system)
	out = appendBool(out, "ecs_cluster_gc_enabled", g.GCSystemMetadataIsEnabled, system)

	return out
}
