package ecs

import (
	"bytes"
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
func (h *halList[T]) UnmarshalJSON(b []byte) error {
	var raw struct {
		Underscore json.RawMessage `json:"_instances"`
		Documented json.RawMessage `json:"instances"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	under, underSeen, err := decodeInstances[T](raw.Underscore)
	if err != nil {
		return err
	}
	doc, docSeen, err := decodeInstances[T](raw.Documented)
	if err != nil {
		return err
	}
	switch {
	case underSeen:
		h.Instances, h.KeySeen = under, true
		h.Conflict = docSeen && !reflect.DeepEqual(under, doc)
	case docSeen:
		h.Instances, h.KeySeen = doc, true
	}
	return nil
}

// decodeInstances decodes one spelling of the instance array and reports whether
// the key was present with a value. Presence is not tested by length: an empty
// array is a legitimately empty cluster and must still count as a key sighting.
// An explicit null does not — it carries no list, so treating it as a sighting
// would suppress the shape warning on a payload that told us nothing.
func decodeInstances[T any](raw json.RawMessage) (list []T, seen bool, err error) {
	if raw == nil || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, false, err
	}
	return list, true, nil
}

// halShape describes a decoded HAL list's key situation. It exists so the
// warning helper is not generic in the element type, which the callers do not
// care about.
type halShape struct {
	KeySeen  bool
	Conflict bool
}

// Shape reports the key situation for warnHalShape.
func (h halList[T]) Shape() halShape {
	return halShape{KeySeen: h.KeySeen, Conflict: h.Conflict}
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
