package ecs

import (
	"encoding/json"
	"testing"
)

// TestLocalZoneLivePayloadShape decodes an unedited capture from a real
// ObjectScale 4.3 cluster and asserts that every family produces its full,
// hard-coded sample count.
//
// It asserts no values on purpose: the source cluster was idle, so most values
// are zero and any value assertion would be weak. What it proves is that the
// struct tags match a payload the vendor's own cluster actually emits — a
// misspelled tag passes hand-written fixtures, because those carry the same
// misspelling as the code, but it cannot pass this.
//
// The expected counts are hard-coded rather than derived, and deliberately so:
// a family with one misspelled tag still produces a non-zero sample count (one
// fewer than it should), so a "some samples came out" check would not catch it.
// This also means the assertion is brittle by design — if the fixture is ever
// recaptured from a cluster reporting fewer fields, this test must fail and
// force a human to look, rather than silently covering less.
func TestLocalZoneLivePayloadShape(t *testing.T) {
	var z localZoneResp
	if err := json.Unmarshal([]byte(fixture(t, "localzone-live-4.3.json")), &z); err != nil {
		t.Fatalf("decoding the live payload failed: %v", err)
	}

	families := []struct {
		name    string
		samples []Sample
	}{
		{"gc", z.gcFields.samples()},
		{"recovery", z.recoveryFields.samples()},
		{"erasure coding", z.erasureCodingFields.samples()},
		{"allocation components", z.allocationComponentFields.samples()},
	}
	wantCounts := map[string]int{
		"gc":                    10,
		"recovery":              3,
		"erasure coding":        5,
		"allocation components": 5,
	}
	for _, f := range families {
		want := wantCounts[f.name]
		if len(f.samples) != want {
			t.Errorf("%s family produced %d samples from the live payload, want exactly %d: a JSON tag is probably misspelled", f.name, len(f.samples), want)
		}
	}

	// Every emitted series must carry the label keys its metric name declares,
	// or the Prometheus collector drops it at scrape time (ADR-0006).
	wantKeys := map[string][]string{
		"ecs_cluster_gc_pending_bytes":                     {"scope"},
		"ecs_cluster_gc_reclaimed_bytes":                   {"scope"},
		"ecs_cluster_gc_unreclaimable_bytes":               {"scope"},
		"ecs_cluster_gc_detected_bytes":                    {"scope"},
		"ecs_cluster_gc_enabled":                           {"scope"},
		"ecs_cluster_disk_space_allocated_component_bytes": {"purpose"},
	}
	for _, f := range families {
		for _, s := range f.samples {
			want, ok := wantKeys[s.Name]
			if !ok {
				if len(s.Labels) != 0 {
					t.Errorf("%s: expected no labels, got %v", s.Name, s.Labels)
				}
				continue
			}
			if len(s.Labels) != len(want) {
				t.Errorf("%s: got %d labels, want %d", s.Name, len(s.Labels), len(want))
				continue
			}
			for i, key := range want {
				if s.Labels[i].Key != key {
					t.Errorf("%s: label %d key = %q, want %q", s.Name, i, s.Labels[i].Key, key)
				}
			}
		}
	}
}
