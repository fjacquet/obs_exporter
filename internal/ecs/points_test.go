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
