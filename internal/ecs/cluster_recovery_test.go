package ecs

import (
	"encoding/json"
	"testing"
)

func TestRecoveryFieldsSamples(t *testing.T) {
	const payload = `{
		"recoveryBadChunksTotalSizeCurrent": [{"t": "12345678", "Space": "20000"}, {"t": "23456789", "Space": "10992"}],
		"recoveryRateCurrent":               [{"t": "23456789", "Rate": "17.5"}],
		"recoveryCompleteTimeEstimate":      "45.5"
	}`

	var r recoveryFields
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatal(err)
	}
	got := r.samples()

	// "Current" is the newest point by t, not the largest or the first.
	mustSample(t, got, "ecs_cluster_recovery_bad_chunks_bytes", 10992)
	mustSample(t, got, "ecs_cluster_recovery_rate", 17.5)
	mustSample(t, got, "ecs_cluster_recovery_complete_time_estimate", 45.5)
}

func TestRecoveryFieldsSamplesEmptyPayload(t *testing.T) {
	var r recoveryFields
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}

func TestRecoveryFieldsSamplesUnparseableRate(t *testing.T) {
	const payload = `{
		"recoveryBadChunksTotalSizeCurrent": [{"t": "23456789", "Space": "10992"}],
		"recoveryRateCurrent":               [{"t": "23456789", "Rate": "N/A"}]
	}`
	var r recoveryFields
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatal(err)
	}
	got := r.samples()
	if _, ok := findSample(got, "ecs_cluster_recovery_rate"); ok {
		t.Error("an unparseable rate must yield an absent sample, not zero")
	}
	// A zero-byte bad-chunk total is meaningful and must still be emitted.
	mustSample(t, got, "ecs_cluster_recovery_bad_chunks_bytes", 10992)
}
