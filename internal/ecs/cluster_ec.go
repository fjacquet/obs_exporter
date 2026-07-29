package ecs

// erasureCodingFields carries the erasure-coding block of the local-zone
// dashboard: how much data is eligible for EC, how much is already coded, and how
// fast the gap is closing.
//
// chunksEcRate and chunksEcCompleteTimeEstimate carry no unit in the API
// reference, so their metric names carry no unit suffix (ADR-0006).
type erasureCodingFields struct {
	ChunksEcApplicableTotalSealSize Series `json:"chunksEcApplicableTotalSealSizeCurrent"`
	ChunksEcCodedTotalSealSize      Series `json:"chunksEcCodedTotalSealSizeCurrent"`
	ChunksEcCodedRatio              Series `json:"chunksEcCodedRatioCurrent"`
	ChunksEcRate                    Series `json:"chunksEcRateCurrent"`
	ChunksEcCompleteTimeEstimate    Num    `json:"chunksEcCompleteTimeEstimate"`
}

// samples maps the erasure-coding block to cluster-agnostic samples. Missing or
// unparseable values yield absent samples, never zeros.
func (e erasureCodingFields) samples() []Sample {
	var out []Sample
	out = appendSeries(out, "ecs_cluster_ec_applicable_bytes", e.ChunksEcApplicableTotalSealSize)
	out = appendSeries(out, "ecs_cluster_ec_coded_bytes", e.ChunksEcCodedTotalSealSize)
	out = appendSeries(out, "ecs_cluster_ec_coded_ratio_percent", e.ChunksEcCodedRatio)
	out = appendSeries(out, "ecs_cluster_ec_rate", e.ChunksEcRate)
	out = appendNum(out, "ecs_cluster_ec_complete_time_estimate", e.ChunksEcCompleteTimeEstimate)
	return out
}
