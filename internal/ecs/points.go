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

// isAbsentToken reports whether a trimmed token is one of the values ECS uses to
// mean "no reading", rather than a number it failed to format. This is the single
// place that set is defined: every tolerant decode path in this file consults it,
// so adding a sentinel here covers scalars and series alike.
func isAbsentToken(s string) bool {
	return s == "" || s == "null" || strings.EqualFold(s, "n/a")
}

// cleanScalar unwraps a raw JSON scalar the ECS API may have quoted, and reports
// whether anything decodable is left.
func cleanScalar(raw []byte) (string, bool) {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	if isAbsentToken(s) {
		return "", false
	}
	return s, true
}

// UnmarshalJSON implements tolerant number decoding.
func (n *Num) UnmarshalJSON(b []byte) error {
	s, ok := cleanScalar(b)
	if !ok {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	n.Val, n.Set = v, true
	return nil
}

// Bool is a flag the ECS API encodes as a quoted string ("true"/"false"), which
// Num deliberately refuses. Unparseable values (including "N/A", "", null) leave
// Set false rather than failing the whole decode, so a flag the cluster does not
// report yields an absent sample rather than a misleading false.
type Bool struct {
	Val bool
	Set bool
}

// UnmarshalJSON implements tolerant boolean decoding.
func (b *Bool) UnmarshalJSON(raw []byte) error {
	s, ok := cleanScalar(raw)
	if !ok {
		return nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return nil
	}
	b.Val, b.Set = v, true
	return nil
}

// anyToFloat converts a raw-decoded JSON value (float64 or string) to a float.
func anyToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		s := strings.TrimSpace(x)
		if isAbsentToken(s) {
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
