package ecs

import (
	"encoding/json"

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
}

// UnmarshalJSON accepts either spelling of the instance-array key, preferring
// the "_instances" form that real clusters emit when both are present.
func (h *halList[T]) UnmarshalJSON(b []byte) error {
	var raw struct {
		Underscore []T `json:"_instances"`
		Documented []T `json:"instances"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	// Presence is tested against nil rather than length: an empty array is a
	// legitimately empty cluster and must still count as a key sighting.
	switch {
	case raw.Underscore != nil:
		h.Instances, h.KeySeen = raw.Underscore, true
	case raw.Documented != nil:
		h.Instances, h.KeySeen = raw.Documented, true
	}
	return nil
}

// warnUnknownHalShape logs when a HAL payload carried neither spelling of the
// instance-array key, so an unrecognised shape leaves a trace instead of
// silently yielding zero instances. An empty-but-present list is not a warning:
// that is a legitimately empty cluster.
//
// The cluster is included because this exporter polls many clusters per
// cycle: a warning naming only the endpoint path would not tell an operator
// which cluster drifted.
//
// This is deliberately a warning and not an error: a build that omits
// "_embedded" entirely on an empty cluster would be indistinguishable from
// shape drift, and a false ecs_collector_up=0 is worse than a missed alert.
func warnUnknownHalShape(cluster, path string, keySeen bool) {
	if keySeen {
		return
	}
	log.WithFields(log.Fields{"cluster": cluster, "path": path}).
		Warn("HAL instance list key not found (_instances/instances); payload shape may have changed")
}
