package ecs

import (
	"encoding/json"
	"testing"
)

func TestGCFieldsSamples(t *testing.T) {
	const payload = `{
		"gcUserPendingCurrent":        [{"t": "12345678", "Capacity": "700"}, {"t": "23456789", "Capacity": "900"}],
		"gcUserReclaimedCurrent":      [{"t": "23456789", "Capacity": "8100"}],
		"gcUserUnreclaimableCurrent":  [{"t": "23456789", "Capacity": "640"}],
		"gcUserTotalDetectedCurrent":  [{"t": "23456789", "Capacity": "9700"}],
		"gcUserDataIsEnabled":         "true",
		"gcSystemPendingCurrent":      [{"t": "23456789", "Capacity": "130"}],
		"gcSystemReclaimedCurrent":    [{"t": "23456789", "Capacity": "2500"}],
		"gcSystemUnreclaimableCurrent":[{"t": "23456789", "Capacity": "70"}],
		"gcSystemTotalDetectedCurrent":[{"t": "23456789", "Capacity": "2600"}]
	}`

	var g gcFields
	if err := json.Unmarshal([]byte(payload), &g); err != nil {
		t.Fatal(err)
	}
	got := g.samples()

	user := Label{"scope", "user"}
	system := Label{"scope", "system"}

	// "Current" is the newest point by t, not the first.
	mustSample(t, got, "ecs_cluster_gc_bytes", 900, user, Label{"state", "pending"})
	mustSample(t, got, "ecs_cluster_gc_reclaimed_bytes_total", 8100, user)
	mustSample(t, got, "ecs_cluster_gc_bytes", 640, user, Label{"state", "unreclaimable"})
	mustSample(t, got, "ecs_cluster_gc_detected_bytes_total", 9700, user)
	mustSample(t, got, "ecs_cluster_gc_enabled", 1, user)

	mustSample(t, got, "ecs_cluster_gc_bytes", 130, system, Label{"state", "pending"})
	mustSample(t, got, "ecs_cluster_gc_reclaimed_bytes_total", 2500, system)
	mustSample(t, got, "ecs_cluster_gc_bytes", 70, system, Label{"state", "unreclaimable"})
	mustSample(t, got, "ecs_cluster_gc_detected_bytes_total", 2600, system)

	// gcSystemMetadataIsEnabled was absent: the sample must be absent, not 0.
	if _, ok := findSample(got, "ecs_cluster_gc_enabled", system); ok {
		t.Error("gc_enabled{scope=system} must be absent when the flag is not reported")
	}
}

func TestGCFieldsSamplesDisabledFlagIsZeroNotAbsent(t *testing.T) {
	var g gcFields
	if err := json.Unmarshal([]byte(`{"gcUserDataIsEnabled": "false"}`), &g); err != nil {
		t.Fatal(err)
	}
	// A reported "false" is real information and must be emitted as 0 — only an
	// unreported flag is absent.
	mustSample(t, g.samples(), "ecs_cluster_gc_enabled", 0, Label{"scope", "user"})
}

func TestGCFieldsSamplesEmptyPayload(t *testing.T) {
	var g gcFields
	if err := json.Unmarshal([]byte(`{}`), &g); err != nil {
		t.Fatal(err)
	}
	if got := g.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}

func TestGCFieldsSamplesUnparseableValue(t *testing.T) {
	const payload = `{
		"gcUserPendingCurrent":   [{"t": "23456789", "Capacity": "N/A"}],
		"gcUserReclaimedCurrent": [{"t": "23456789", "Capacity": "8100"}]
	}`
	var g gcFields
	if err := json.Unmarshal([]byte(payload), &g); err != nil {
		t.Fatal(err)
	}
	got := g.samples()
	user := Label{"scope", "user"}
	if _, ok := findSample(got, "ecs_cluster_gc_pending_bytes", user); ok {
		t.Error("an unparseable value must yield an absent sample, not zero")
	}
	// The rest of the family still emits.
	mustSample(t, got, "ecs_cluster_gc_reclaimed_bytes_total", 8100, user)
}
