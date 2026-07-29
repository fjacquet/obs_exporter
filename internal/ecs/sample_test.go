package ecs

import "testing"

// The append helpers are asserted through mustSample/findSample, by metric
// identity rather than by position in the slice: real consumers look samples up
// by name and labels, so a test coupled to append order would break on a
// reordering that changes nothing observable. Each case uses its own metric name
// so the lookups stay unambiguous.

func TestAppendSeries(t *testing.T) {
	var out []Sample

	// The newest point by t wins, not the first or the largest.
	out = appendSeries(out, "ecs_unlabelled_bytes", Series{
		{"t": "100", "Capacity": "5"},
		{"t": "200", "Capacity": "9"},
	})
	mustSample(t, out, "ecs_unlabelled_bytes", 9)
	if s, ok := findSample(out, "ecs_unlabelled_bytes"); ok && len(s.Labels) != 0 {
		t.Errorf("got %d labels, want none when no labels are passed", len(s.Labels))
	}

	// Labels are carried through to the sample.
	out = appendSeries(out, "ecs_labelled_bytes", Series{{"t": "1", "Capacity": "3"}},
		Label{"scope", "user"})
	mustSample(t, out, "ecs_labelled_bytes", 3, Label{"scope", "user"})

	// Nothing decodable appends nothing — absent, never zero.
	for _, tc := range []struct {
		name   string
		series Series
	}{
		{"ecs_empty_series_bytes", Series{}},
		{"ecs_unparseable_series_bytes", Series{{"t": "1", "Capacity": "N/A"}}},
	} {
		before := len(out)
		out = appendSeries(out, tc.name, tc.series)
		if len(out) != before {
			t.Errorf("%s: appended %d samples, want 0", tc.name, len(out)-before)
		}
		if _, ok := findSample(out, tc.name); ok {
			t.Errorf("%s: must be absent, not zero", tc.name)
		}
	}
}

func TestAppendNum(t *testing.T) {
	var out []Sample

	out = appendNum(out, "ecs_estimate_seconds", Num{Val: 45.5, Set: true})
	mustSample(t, out, "ecs_estimate_seconds", 45.5)

	// A parsed zero is real data and must be emitted, labels and all.
	out = appendNum(out, "ecs_zero_bytes", Num{Val: 0, Set: true}, Label{"purpose", "x"})
	mustSample(t, out, "ecs_zero_bytes", 0, Label{"purpose", "x"})

	// An unset Num appends nothing — absent, never zero.
	before := len(out)
	out = appendNum(out, "ecs_unset_bytes", Num{})
	if len(out) != before {
		t.Errorf("an unset Num appended %d samples, want 0", len(out)-before)
	}
	if _, ok := findSample(out, "ecs_unset_bytes"); ok {
		t.Error("an unset Num must be absent, not zero")
	}
}

func TestAppendBool(t *testing.T) {
	var out []Sample

	out = appendBool(out, "ecs_on", Bool{Val: true, Set: true}, Label{"scope", "user"})
	mustSample(t, out, "ecs_on", 1, Label{"scope", "user"})

	// A reported false is 0, not absence: "switched off" is real information, and
	// collapsing it into absence is the mistake this case exists to catch.
	out = appendBool(out, "ecs_off", Bool{Val: false, Set: true}, Label{"scope", "system"})
	mustSample(t, out, "ecs_off", 0, Label{"scope", "system"})

	// Only silence about the flag is absence.
	before := len(out)
	out = appendBool(out, "ecs_unreported", Bool{})
	if len(out) != before {
		t.Errorf("an unset Bool appended %d samples, want 0", len(out)-before)
	}
	if _, ok := findSample(out, "ecs_unreported"); ok {
		t.Error("an unreported flag must be absent, not zero")
	}
}
