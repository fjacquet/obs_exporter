package ecs

import (
	"encoding/json"
	"testing"
)

func TestSeriesLatest(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want float64
		ok   bool
	}{
		{"numbers", `[{"t":"1","Space":100},{"t":"3","Space":50},{"t":"2","Space":75}]`, 50, true},
		{"string values", `[{"t":"1","Count":"4"},{"t":"2","Count":"7"}]`, 7, true},
		{"stray space keys", `[{"t":"1"," Space ":"42"}]`, 42, true},
		{"numeric t", `[{"t":1502827401,"Bytes":"10"}]`, 10, true},
		{"empty", `[]`, 0, false},
		{"na value", `[{"t":"1","Percent":"N/A"}]`, 0, false},
		{"mixed na and value", `[{"t":"1","Rate":"N/A"},{"t":"2","Rate":6}]`, 6, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s Series
			if err := json.Unmarshal([]byte(c.in), &s); err != nil {
				t.Fatal(err)
			}
			got, ok := s.Latest()
			if ok != c.ok || got != c.want {
				t.Errorf("Latest() = (%v, %v), want (%v, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestNumUnmarshal(t *testing.T) {
	var v struct {
		A Num `json:"a"`
		B Num `json:"b"`
		C Num `json:"c"`
		D Num `json:"d"`
	}
	in := `{"a": "42", "b": 7.5, "c": "N/A", "d": -1}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if !v.A.Set || v.A.Val != 42 {
		t.Errorf("A = %+v", v.A)
	}
	if !v.B.Set || v.B.Val != 7.5 {
		t.Errorf("B = %+v", v.B)
	}
	if v.C.Set {
		t.Errorf("C should be unset: %+v", v.C)
	}
	if !v.D.Set || v.D.Val != -1 {
		t.Errorf("D = %+v", v.D)
	}
}

func TestBoolUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantVal bool
		wantSet bool
	}{
		{name: "quoted true, as the dashboard sends it", payload: `"true"`, wantVal: true, wantSet: true},
		{name: "quoted false", payload: `"false"`, wantVal: false, wantSet: true},
		{name: "native JSON true", payload: `true`, wantVal: true, wantSet: true},
		{name: "native JSON false", payload: `false`, wantVal: false, wantSet: true},
		{name: "mixed case", payload: `"True"`, wantVal: true, wantSet: true},
		{name: "N/A leaves it unset", payload: `"N/A"`, wantSet: false},
		{name: "empty string leaves it unset", payload: `""`, wantSet: false},
		{name: "null leaves it unset", payload: `null`, wantSet: false},
		{name: "unrecognised word leaves it unset", payload: `"maybe"`, wantSet: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got Bool
			if err := json.Unmarshal([]byte(tc.payload), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Set != tc.wantSet {
				t.Fatalf("Set = %v, want %v", got.Set, tc.wantSet)
			}
			if got.Set && got.Val != tc.wantVal {
				t.Errorf("Val = %v, want %v", got.Val, tc.wantVal)
			}
		})
	}
}

// A JSON null inside a series point means "no reading", exactly as it does for a
// scalar. Before isAbsentToken existed, anyToFloat rejected "" and "N/A" but not
// "null", so the two decode paths disagreed on what counts as absent.
func TestSeriesTreatsNullAsAbsent(t *testing.T) {
	for _, tc := range []struct{ name, token string }{
		{"null", "null"},
		{"empty", ""},
		{"not available", "N/A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Series{{"t": "1", "Capacity": tc.token}}
			if v, ok := s.Latest(); ok {
				t.Errorf("Latest() = %v, true; want absent for %q", v, tc.token)
			}
		})
	}
}

// "Current" means the newest point, not the newest point that happens to parse.
// Falling back to an older reading publishes stale data under a live gauge, which
// is the time-axis form of the absent-never-zero rule (ADR-0007): a value we
// cannot read now must be absent, not quietly replaced by one from before.
func TestLatestDoesNotFallBackToAnOlderPoint(t *testing.T) {
	tests := []struct {
		name    string
		series  Series
		wantVal float64
		wantOK  bool
	}{
		{
			name:    "newest parses",
			series:  Series{{"t": "100", "Space": "5"}, {"t": "200", "Space": "9"}},
			wantVal: 9, wantOK: true,
		},
		{
			name:   "newest unparseable, older valid: absent rather than stale",
			series: Series{{"t": "100", "Space": "500"}, {"t": "200", "Space": "N/A"}},
			wantOK: false,
		},
		{
			name:   "newest is null, older valid: still absent",
			series: Series{{"t": "100", "Space": "500"}, {"t": "200", "Space": "null"}},
			wantOK: false,
		},
		{
			// Order in the payload must not matter — only t does.
			name:   "unparseable newest listed first",
			series: Series{{"t": "200", "Space": "N/A"}, {"t": "100", "Space": "500"}},
			wantOK: false,
		},
		{
			name:    "single valid point",
			series:  Series{{"t": "1", "Capacity": "42"}},
			wantVal: 42, wantOK: true,
		},
		{name: "empty series", series: Series{}, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.series.Latest()
			if ok != tc.wantOK {
				t.Fatalf("present = %v, want %v (value %v)", ok, tc.wantOK, got)
			}
			if ok && got != tc.wantVal {
				t.Errorf("value = %v, want %v", got, tc.wantVal)
			}
		})
	}
}
