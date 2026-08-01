package ecs

import (
	"reflect"
	"testing"
)

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

// nodes.go and replication.go build one label set per instance and spread it
// into a dozen appends, so the helpers must copy it: sharing the caller's
// backing array would let a later edit rewrite samples already emitted. Passing
// labels as literals cannot catch that — Go allocates a fresh slice per call —
// so each case spreads a slice it then mutates, which is the real call shape.
func TestAppendHelpersDetachLabelSlices(t *testing.T) {
	for _, tc := range []struct {
		name string
		emit func(out []Sample, name string, labels ...Label) []Sample
	}{
		{"ecs_detached_series_bytes", func(out []Sample, name string, labels ...Label) []Sample {
			return appendSeries(out, name, Series{{"t": "1", "Capacity": "1"}}, labels...)
		}},
		{"ecs_detached_num_bytes", func(out []Sample, name string, labels ...Label) []Sample {
			return appendNum(out, name, Num{Val: 1, Set: true}, labels...)
		}},
		{"ecs_detached_bool", func(out []Sample, name string, labels ...Label) []Sample {
			return appendBool(out, name, Bool{Val: true, Set: true}, labels...)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shared := []Label{{"rg", "old"}}
			out := tc.emit(nil, tc.name, shared...)
			shared[0].Value = "rewritten"
			mustSample(t, out, tc.name, 1, Label{"rg", "old"})
		})
	}
}

func TestWithClusterPreservesType(t *testing.T) {
	// The collection loop stamps the cluster label on every sample. A counter
	// that loses its type there would silently export as a gauge.
	s := Sample{Name: "ecs_node_requests_total", Value: 1, Type: Counter}
	if got := s.WithCluster("c1").Type; got != Counter {
		t.Errorf("Type after WithCluster = %v, want Counter", got)
	}
}

func TestSampleTypeZeroValueIsGauge(t *testing.T) {
	// Every existing collector builds Samples without a Type; they must stay gauges.
	if (Sample{}).Type != Gauge {
		t.Error("the zero SampleType must be Gauge")
	}
}

func TestWithIdentityOrder(t *testing.T) {
	s := Sample{
		Name:   "ecs_namespace_used_bytes",
		Labels: []Label{{Key: "namespace", Value: "ns1"}},
		Value:  42,
	}
	extra := []Label{{Key: "env", Value: "prod"}, {Key: "site", Value: "geneva"}}

	got := s.WithIdentity("c1", extra)
	want := []Label{
		{Key: "cluster", Value: "c1"},
		{Key: "env", Value: "prod"},
		{Key: "site", Value: "geneva"},
		{Key: "namespace", Value: "ns1"},
	}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
	if got.Name != s.Name || got.Value != s.Value {
		t.Errorf("name/value not preserved: %+v", got)
	}
}

func TestWithIdentitySkipsCollisions(t *testing.T) {
	s := Sample{Name: "ecs_node_health_state", Labels: []Label{{Key: "node", Value: "n1"}}}
	extra := []Label{
		{Key: "cluster", Value: "wrong"},
		{Key: "env", Value: "prod"},
		{Key: "node", Value: "wrong"},
	}

	got := s.WithIdentity("c1", extra)
	want := []Label{
		{Key: "cluster", Value: "c1"},
		{Key: "env", Value: "prod"},
		{Key: "node", Value: "n1"},
	}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}

	collisions := s.CollidingLabels(extra)
	if !reflect.DeepEqual(collisions, []string{"cluster", "node"}) {
		t.Errorf("CollidingLabels = %v, want [cluster node]", collisions)
	}
}

func TestWithIdentityNilExtraMatchesWithCluster(t *testing.T) {
	s := Sample{Name: "ecs_up", Labels: []Label{{Key: "collector", Value: "cluster"}}, Value: 1}
	if !reflect.DeepEqual(s.WithIdentity("c1", nil).Labels, s.WithCluster("c1").Labels) {
		t.Error("WithIdentity(name, nil) must match WithCluster(name)")
	}
}

func TestWithIdentityKeepsEmptyValuedCollision(t *testing.T) {
	// A collector dimension whose value is empty is still a dimension: the
	// custom label must be skipped, not merged over it.
	s := Sample{Name: "ecs_cluster_alerts", Labels: []Label{{Key: "severity", Value: ""}}}
	got := s.WithIdentity("c1", []Label{{Key: "severity", Value: "critical"}})
	want := []Label{{Key: "cluster", Value: "c1"}, {Key: "severity", Value: ""}}
	if !reflect.DeepEqual(got.Labels, want) {
		t.Errorf("labels = %v, want %v", got.Labels, want)
	}
}
