package ecs

import "testing"

func TestAppendSeries(t *testing.T) {
	var out []Sample

	// A parseable series appends one sample carrying the newest point.
	out = appendSeries(out, "ecs_thing_bytes", Series{
		{"t": "100", "Capacity": "5"},
		{"t": "200", "Capacity": "9"},
	})
	if len(out) != 1 {
		t.Fatalf("got %d samples, want 1", len(out))
	}
	if out[0].Name != "ecs_thing_bytes" || out[0].Value != 9 {
		t.Errorf("got %s = %v, want ecs_thing_bytes = 9", out[0].Name, out[0].Value)
	}
	if len(out[0].Labels) != 0 {
		t.Errorf("got %d labels, want none when no labels are passed", len(out[0].Labels))
	}

	// Labels are carried through in the order given.
	out = appendSeries(out, "ecs_thing_bytes", Series{{"t": "1", "Capacity": "3"}},
		Label{"scope", "user"})
	if len(out) != 2 {
		t.Fatalf("got %d samples, want 2", len(out))
	}
	if got := out[1].LabelValue("scope"); got != "user" {
		t.Errorf("scope label = %q, want %q", got, "user")
	}

	// An empty series appends nothing — absent, never zero.
	before := len(out)
	out = appendSeries(out, "ecs_thing_bytes", Series{})
	if len(out) != before {
		t.Errorf("an empty series appended %d samples, want 0", len(out)-before)
	}

	// An unparseable value appends nothing either.
	out = appendSeries(out, "ecs_thing_bytes", Series{{"t": "1", "Capacity": "N/A"}})
	if len(out) != before {
		t.Errorf("an unparseable series appended %d samples, want 0", len(out)-before)
	}
}

func TestAppendNum(t *testing.T) {
	var out []Sample

	out = appendNum(out, "ecs_thing_seconds", Num{Val: 45.5, Set: true})
	if len(out) != 1 {
		t.Fatalf("got %d samples, want 1", len(out))
	}
	if out[0].Value != 45.5 {
		t.Errorf("value = %v, want 45.5", out[0].Value)
	}

	out = appendNum(out, "ecs_thing_seconds", Num{Val: 0, Set: true}, Label{"purpose", "x"})
	if len(out) != 2 {
		t.Fatalf("got %d samples, want 2", len(out))
	}
	// A parsed zero is real data and must be emitted.
	if out[1].Value != 0 || out[1].LabelValue("purpose") != "x" {
		t.Errorf("got %v/%q, want 0 with purpose=x", out[1].Value, out[1].LabelValue("purpose"))
	}

	// An unset Num appends nothing — absent, never zero.
	before := len(out)
	out = appendNum(out, "ecs_thing_seconds", Num{})
	if len(out) != before {
		t.Errorf("an unset Num appended %d samples, want 0", len(out)-before)
	}
}
