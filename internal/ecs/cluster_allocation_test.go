package ecs

import (
	"encoding/json"
	"testing"
)

func TestAllocationComponentFieldsSamples(t *testing.T) {
	const payload = `{
		"diskSpaceAllocatedUserDataCurrent":        [{"t": "23456789", "Capacity": "3100"}],
		"diskSpaceAllocatedSystemMetadataCurrent":  [{"t": "23456789", "Capacity": "1200"}],
		"diskSpaceAllocatedLocalProtectionCurrent": [{"t": "23456789", "Capacity": "600"}]
	}`

	var a allocationComponentFields
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		t.Fatal(err)
	}
	got := a.samples()

	mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", 3100, Label{"type", "user_data"})
	mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", 1200, Label{"type", "system_metadata"})
	mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", 600, Label{"type", "local_protection"})

	// geo_cache and geo_copy were not reported: they must be absent, not zero.
	// Padding them with zeros would imply the breakdown is exhaustive, which it
	// is not — on a live 4.3 cluster the components sum to 12.8% less than
	// diskSpaceAllocatedCurrent.
	for _, purpose := range []string{"geo_cache", "geo_copy"} {
		if _, ok := findSample(got, "ecs_cluster_disk_space_allocated_component_bytes", Label{"type", purpose}); ok {
			t.Errorf("purpose=%s must be absent when the cluster does not report it", purpose)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d samples, want exactly 3", len(got))
	}
}

func TestAllocationComponentFieldsSamplesAllPurposes(t *testing.T) {
	const payload = `{
		"diskSpaceAllocatedUserDataCurrent":        [{"t": "1", "Capacity": "1"}],
		"diskSpaceAllocatedSystemMetadataCurrent":  [{"t": "1", "Capacity": "2"}],
		"diskSpaceAllocatedGeoCacheCurrent":        [{"t": "1", "Capacity": "3"}],
		"diskSpaceAllocatedGeoCopyCurrent":         [{"t": "1", "Capacity": "4"}],
		"diskSpaceAllocatedLocalProtectionCurrent": [{"t": "1", "Capacity": "5"}]
	}`
	var a allocationComponentFields
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		t.Fatal(err)
	}
	got := a.samples()
	if len(got) != 5 {
		t.Fatalf("got %d samples, want 5 (one per purpose)", len(got))
	}
	for _, tc := range []struct {
		purpose string
		want    float64
	}{
		{"user_data", 1}, {"system_metadata", 2}, {"geo_cache", 3},
		{"geo_copy", 4}, {"local_protection", 5},
	} {
		mustSample(t, got, "ecs_cluster_disk_space_allocated_component_bytes", tc.want, Label{"type", tc.purpose})
	}
}

func TestAllocationComponentFieldsSamplesEmptyPayload(t *testing.T) {
	var a allocationComponentFields
	if err := json.Unmarshal([]byte(`{}`), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.samples(); len(got) != 0 {
		t.Errorf("got %d samples from an empty payload, want 0", len(got))
	}
}
