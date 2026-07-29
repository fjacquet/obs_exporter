package ecs

import (
	"encoding/json"
	"testing"
)

func TestErasureCodingFieldsSamples(t *testing.T) {
	const payload = `{
		"chunksEcApplicableTotalSealSizeCurrent": [{"t": "23456789", "Space": "59000"}],
		"chunksEcCodedTotalSealSizeCurrent":      [{"t": "23456789", "Space": "58000"}],
		"chunksEcCodedRatioCurrent":              [{"t": "12345678", "Percent": "97.5"}, {"t": "23456789", "Percent": "98.3"}],
		"chunksEcRateCurrent":                    [{"t": "23456789", "Rate": "12.5"}],
		"chunksEcCompleteTimeEstimate":           "3.25"
	}`

	var e erasureCodingFields
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		t.Fatal(err)
	}
	got := e.samples()

	mustSample(t, got, "ecs_cluster_ec_applicable_bytes", 59000)
	mustSample(t, got, "ecs_cluster_ec_coded_bytes", 58000)
	// Newest point by t wins.
	mustSample(t, got, "ecs_cluster_ec_coded_ratio_percent", 98.3)
	mustSample(t, got, "ecs_cluster_ec_rate", 12.5)
	mustSample(t, got, "ecs_cluster_ec_complete_time_estimate", 3.25)
}

func TestErasureCodingFieldsSamplesEmptyPayload(t *testing.T) {
	var e erasureCodingFields
	if err := json.Unmarshal([]byte(`{}`), &e); err != nil {
		t.Fatal(err)
	}
	if got := e.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}

func TestErasureCodingFieldsSamplesPartialPayload(t *testing.T) {
	// A cluster reporting only the ratio must still yield that one sample.
	const payload = `{"chunksEcCodedRatioCurrent": [{"t": "23456789", "Percent": "98.3"}]}`
	var e erasureCodingFields
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		t.Fatal(err)
	}
	got := e.samples()
	if len(got) != 1 {
		t.Fatalf("got %d samples, want exactly 1", len(got))
	}
	mustSample(t, got, "ecs_cluster_ec_coded_ratio_percent", 98.3)
}
