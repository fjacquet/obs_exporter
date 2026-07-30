package ecs

import (
	"encoding/json"
	"reflect"

	log "github.com/sirupsen/logrus"
)

// halList decodes a HAL "_embedded" instance list.
//
// Real ECS/ObjectScale clusters key the array "_instances" (underscore) —
// field-confirmed from ECS 3.8 through ObjectScale 4.3. The Dell REST API
// reference examples show it without the underscore ("instances"). The bundled
// swagger cannot arbitrate: every response body in it declares an empty schema
// (see ADR-0008), so neither form can be proven from the spec.
//
// Both keys are therefore accepted. Picking only one is not a cosmetic choice:
// a key mismatch decodes zero instances and returns no error, so the collector
// emits no samples while ecs_collector_up still reports 1 — the worst failure
// mode this exporter has. v2.7.0 hit it by reading only "instances", then fixed
// that by switching to "_instances", which left clusters emitting the documented
// form exposed to the same failure. v2.7.1 accepts both and closes the class.
type halList[T any] struct {
	// Instances holds the decoded array, empty when the payload carried none.
	Instances []T
	// halShape is embedded rather than mirrored into a parallel struct: a new
	// way for a list to be untrustworthy is then one field to add, not one field
	// plus a copy in an adapter that compiles fine when you forget it.
	halShape
}

// halShape describes a decoded HAL list's key situation, without the element
// type, so the warning helper does not have to be generic in something it never
// looks at.
type halShape struct {
	// KeySeen reports whether either spelling of the array key was present.
	// False means the payload shape is unrecognised, which callers surface as
	// a warning; it is distinct from a present-but-empty list.
	KeySeen bool
	// Conflict reports that both spellings were present AND carried different
	// arrays, so preferring "_instances" discarded the other one. Callers surface
	// it as a warning: the preference is still the right call on such a payload,
	// but dropping data must not be silent.
	Conflict bool
}

// UnmarshalJSON accepts either spelling of the instance-array key, preferring
// the "_instances" form that real clusters emit when both are present.
//
// Presence is tested against nil, which encoding/json already gives us for free
// with the exact semantics needed: a present-but-empty array decodes to a
// non-nil empty slice (a legitimately empty cluster, so a key sighting), while
// an absent key and an explicit null both leave the field nil (no list, so no
// sighting and the shape warning still fires).
func (h *halList[T]) UnmarshalJSON(b []byte) error {
	var raw struct {
		Underscore []T `json:"_instances"`
		Documented []T `json:"instances"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch {
	case raw.Underscore != nil:
		h.Instances, h.KeySeen = raw.Underscore, true
		h.Conflict = raw.Documented != nil && !reflect.DeepEqual(raw.Underscore, raw.Documented)
	case raw.Documented != nil:
		h.Instances, h.KeySeen = raw.Documented, true
	}
	return nil
}

// warnHalShape logs the two ways a HAL instance list can be untrustworthy, so
// neither leaves the collector quietly reporting less than the cluster sent:
//
//   - neither spelling of the array key was present — an unrecognised shape that
//     otherwise yields zero instances with no error at all. An empty-but-present
//     list is NOT this case: that is a legitimately empty cluster.
//   - both spellings were present with different arrays — malformed HAL, never
//     observed in the field. Preferring "_instances" is still correct, but the
//     discarded alternative is worth a line in the log.
//
// The cluster is included because this exporter polls many clusters per
// cycle: a warning naming only the endpoint path would not tell an operator
// which cluster drifted.
//
// These are deliberately warnings and not errors: a build that omits
// "_embedded" entirely on an empty cluster would be indistinguishable from
// shape drift, and a false ecs_collector_up=0 is worse than a missed alert.
func warnHalShape(cluster, path string, s halShape) {
	// The silent path is every call in normal operation; returning before
	// building the field map keeps it allocation-free.
	if s.KeySeen && !s.Conflict {
		return
	}
	fields := log.Fields{"cluster": cluster, "path": path}
	if !s.KeySeen {
		log.WithFields(fields).
			Warn("HAL instance list key not found (_instances/instances); payload shape may have changed")
	}
	if s.Conflict {
		log.WithFields(fields).
			Warn("HAL payload carried both _instances and instances with different contents; used _instances")
	}
}
