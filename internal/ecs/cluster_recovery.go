package ecs

// recoveryFields carries the chunk-recovery block of the local-zone dashboard.
// recoveryBadChunksTotalSize is a durability signal: bytes of corrupted chunks
// still awaiting recovery.
//
// recoveryRate and recoveryCompleteTimeEstimate carry no unit in the API
// reference, so their metric names carry no unit suffix (ADR-0006).
type recoveryFields struct {
	RecoveryBadChunksTotalSize   Series `json:"recoveryBadChunksTotalSizeCurrent"`
	RecoveryRate                 Series `json:"recoveryRateCurrent"`
	RecoveryCompleteTimeEstimate Num    `json:"recoveryCompleteTimeEstimate"`
}

// samples maps the recovery block to cluster-agnostic samples. Missing or
// unparseable values yield absent samples, never zeros.
func (r recoveryFields) samples() []Sample {
	var out []Sample
	out = appendSeries(out, "ecs_cluster_recovery_bad_chunks_bytes", r.RecoveryBadChunksTotalSize)
	out = appendSeries(out, "ecs_cluster_recovery_rate", r.RecoveryRate)
	out = appendNum(out, "ecs_cluster_recovery_complete_time_estimate", r.RecoveryCompleteTimeEstimate)
	return out
}
