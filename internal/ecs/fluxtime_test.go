package ecs

import (
	"testing"
	"time"
)

func rowWithTime(ts string) fluxRow {
	cols := map[string]string{"_field": "usage_user", "_value": "1"}
	if ts != "" {
		cols["_time"] = ts
	}
	return fluxRow{cols: cols}
}

func TestRowAge(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 40, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		ts      string
		wantAge time.Duration
		wantOK  bool
	}{
		{"fresh", "2026-07-31T08:35:09Z", 4*time.Minute + 51*time.Second, true},
		{"fractional seconds", "2026-07-31T08:35:09.481Z", 4*time.Minute + 50*time.Second + 519*time.Millisecond, true},
		{"clock skew puts the point ahead", "2026-07-31T08:41:00Z", -time.Minute, true},
		{"missing column", "", 0, false},
		{"unparseable", "not-a-timestamp", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rowWithTime(tc.ts).age(now)
			if ok != tc.wantOK {
				t.Fatalf("age() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantAge {
				t.Errorf("age() = %v, want %v", got, tc.wantAge)
			}
		})
	}
}

func TestStaleRowYieldsNoSample(t *testing.T) {
	// last() returns the newest point in the window whatever its age, and these
	// samples carry no timestamp, so Prometheus stamps them at scrape time. A
	// node that stopped emitting eleven minutes ago must go absent, not look
	// current. This is ADR-0007 along the time axis.
	q := fluxQuery{
		bucket: "monitoring_op", measurement: "cpu",
		fields: []fluxField{{field: "usage_user", name: "ecs_node_cpu_utilization_percent"}},
	}
	now := time.Date(2026, 7, 31, 8, 40, 0, 0, time.UTC)
	rows := []fluxRow{{cols: map[string]string{
		"_field": "usage_user", "_value": "5.1", "_time": "2026-07-31T08:28:00Z",
	}}}
	out, _, stale, _ := q.samples(rows, nil, now)
	if len(out) != 0 {
		t.Errorf("a 12-minute-old point produced %d samples, want none", len(out))
	}
	if stale != 1 {
		t.Errorf("stale = %v, want 1", stale)
	}
}

func TestFreshRowIsKept(t *testing.T) {
	q := fluxQuery{
		bucket: "monitoring_op", measurement: "cpu",
		fields: []fluxField{{field: "usage_user", name: "ecs_node_cpu_utilization_percent"}},
	}
	now := time.Date(2026, 7, 31, 8, 40, 0, 0, time.UTC)
	rows := []fluxRow{{cols: map[string]string{
		"_field": "usage_user", "_value": "5.1", "_time": "2026-07-31T08:36:00Z",
	}}}
	out, _, stale, _ := q.samples(rows, nil, now)
	if len(out) != 1 || out[0].Value != 5.1 {
		t.Fatalf("samples = %v, want one sample valued 5.1", out)
	}
	if stale != 0 {
		t.Errorf("stale = %v, want 0", stale)
	}
}

func TestRowWithoutTimeIsDropped(t *testing.T) {
	// A row we cannot date cannot be shown to be current, and an undated value
	// published as a live gauge is indistinguishable from a fresh one.
	q := fluxQuery{
		bucket: "monitoring_op", measurement: "cpu",
		fields: []fluxField{{field: "usage_user", name: "ecs_node_cpu_utilization_percent"}},
	}
	rows := []fluxRow{{cols: map[string]string{"_field": "usage_user", "_value": "5.1"}}}
	out, _, stale, _ := q.samples(rows, nil, time.Now())
	if len(out) != 0 {
		t.Errorf("an undated row produced %d samples, want none", len(out))
	}
	if stale != 1 {
		t.Errorf("stale = %v, want 1", stale)
	}
}
