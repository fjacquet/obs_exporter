package ecs

import (
	"strconv"
	"strings"
)

// The ECS dashboard API represents most stats as time-series arrays of points:
//
//	"diskSpaceFree": [ {"t":"12345678", "Space":100}, {"t":"23435455", " Space ":"50"} ]
//
// The value key varies per field (Space, Bytes, Percent, Bandwidth, Latency, TPS,
// Count, Rate, Capacity, diskIO, ...), numbers may be JSON numbers or strings, and
// the documented examples show stray whitespace inside keys. Series and Num parse
// this defensively: a point's value is its single non-"t" key, and any value that
// does not parse as a number (e.g. "N/A") is treated as absent.

// Series is a raw-decoded dashboard time series.
type Series []map[string]any

// Latest returns the value of the most recent point (max "t"), if any.
func (s Series) Latest() (float64, bool) {
	bestT := 0.0
	bestV := 0.0
	found := false
	for _, p := range s {
		t := 0.0
		v := 0.0
		vOK := false
		for k, raw := range p {
			if strings.TrimSpace(k) == "t" {
				if f, ok := anyToFloat(raw); ok {
					t = f
				}
				continue
			}
			if f, ok := anyToFloat(raw); ok && !vOK {
				v, vOK = f, true
			}
		}
		if vOK && (!found || t >= bestT) {
			bestT, bestV, found = t, v, true
		}
	}
	return bestV, found
}

// Num is a scalar that the ECS API may encode as a JSON number or a quoted string
// ("4", "1990894400", "true"-like fields excluded). Unparseable values (including
// "N/A", "", null) leave Set false rather than failing the whole decode.
type Num struct {
	Val float64
	Set bool
}

// UnmarshalJSON implements tolerant number decoding.
func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(b)), `"`))
	if s == "" || s == "null" || strings.EqualFold(s, "n/a") {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	n.Val, n.Set = v, true
	return nil
}

// anyToFloat converts a raw-decoded JSON value (float64 or string) to a float.
func anyToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" || strings.EqualFold(s, "n/a") {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
