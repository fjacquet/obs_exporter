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

// Latest returns the value of the most recent point (max "t"), if it is readable.
//
// The newest point is chosen first, and only then is its value parsed: an
// unreadable newest reading yields absence, never the value of an older point.
// This is the absent-never-zero rule of ADR-0007 applied to the time axis — a
// value we cannot read *now* must not be silently replaced by one from before,
// because the exporter publishes these as live gauges and a stale reading is
// indistinguishable from a current one once it reaches Prometheus.
func (s Series) Latest() (float64, bool) {
	newest := -1
	bestT := 0.0
	for i, p := range s {
		t := 0.0
		for k, raw := range p {
			if strings.TrimSpace(k) != "t" {
				continue
			}
			if f, ok := anyToFloat(raw); ok {
				t = f
			}
			break
		}
		// Ties keep the later element, matching the previous behaviour.
		if newest < 0 || t >= bestT {
			newest, bestT = i, t
		}
	}
	if newest < 0 {
		return 0, false
	}
	// A point's value is its single non-"t" key. The loop tolerates more than one
	// only so key order cannot decide the outcome.
	for k, raw := range s[newest] {
		if strings.TrimSpace(k) == "t" {
			continue
		}
		if f, ok := anyToFloat(raw); ok {
			return f, true
		}
	}
	return 0, false
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
