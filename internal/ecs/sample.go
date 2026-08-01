// Package ecs holds the ECS metric model, snapshot store, modular resource
// collectors, and the Prometheus + OTLP export paths.
package ecs

// Label is a single metric label key/value.
type Label struct {
	Key   string
	Value string
}

// SampleType distinguishes a monotonic counter from a gauge. The zero value is
// Gauge, so every collector that predates this type keeps emitting gauges with
// no change.
type SampleType uint8

const (
	// Gauge is a value that may move in either direction.
	Gauge SampleType = iota
	// Counter is a cumulative value that only increases, and restarts from zero
	// when the process producing it restarts — which is exactly what ObjectScale
	// documents for the monitoring_main fields. Consumers rate() it.
	Counter
)

// Sample is one metric data point: a name, an ordered label set, and a value.
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
	Type   SampleType
}

// LabelValue returns the value of the named label, or "" if absent.
func (s Sample) LabelValue(key string) string {
	for _, l := range s.Labels {
		if l.Key == key {
			return l.Value
		}
	}
	return ""
}

// WithCluster returns a copy with a leading {cluster=name} identity label.
// Collectors emit cluster-agnostic samples; the collection loop stamps the
// cluster identity so one exporter process can serve many clusters.
func (s Sample) WithCluster(name string) Sample { return s.WithIdentity(name, nil) }

// WithIdentity returns a copy carrying the {cluster=name} identity label first,
// then the operator's custom labels, then the sample's own collector labels.
// The caller passes extra already sorted by key: ADR-0006 makes the ordered
// label-key set part of a metric's schema.
//
// A custom label is skipped when its key is the reserved cluster identity or a
// dimension the sample already carries — the collector's own dimension wins.
// ADR-0006 guarantees one key set per metric name, so a skip applies uniformly
// to every series of that name and the label-key invariant still holds; the
// custom label is simply absent from that metric family. The collection loop
// logs the skip, so it is not silent.
func (s Sample) WithIdentity(name string, extra []Label) Sample {
	labels := make([]Label, 0, len(s.Labels)+len(extra)+1)
	labels = append(labels, Label{Key: "cluster", Value: name})
	for _, l := range extra {
		if l.Key == "cluster" || s.hasLabel(l.Key) {
			continue
		}
		labels = append(labels, l)
	}
	labels = append(labels, s.Labels...)
	return Sample{Name: s.Name, Labels: labels, Value: s.Value, Type: s.Type}
}

// CollidingLabels returns the keys of extra that WithIdentity will skip for this
// sample, in the order they appear in extra.
func (s Sample) CollidingLabels(extra []Label) []string {
	var out []string
	for _, l := range extra {
		if l.Key == "cluster" || s.hasLabel(l.Key) {
			out = append(out, l.Key)
		}
	}
	return out
}

// hasLabel reports whether the sample carries the key, regardless of its value.
// LabelValue cannot answer this: a dimension with an empty value is still a
// dimension.
func (s Sample) hasLabel(key string) bool {
	for _, l := range s.Labels {
		if l.Key == key {
			return true
		}
	}
	return false
}

// copyLabels detaches a sample from the caller's label slice, as WithCluster
// already does.
//
// Every caller now passes discrete Label values, so the variadic slice is built
// by the compiler per call and no caller can retain it — the copy defends
// against nothing those call sites do today. It is kept because the hazard it
// guards is real and returns the moment someone writes `labels...` again:
// one label set built per loop iteration and spread into several appends would
// have every sample sharing one backing array, and a later edit that reused it
// would silently rewrite samples already emitted.
func copyLabels(labels []Label) []Label {
	if len(labels) == 0 {
		return nil
	}
	out := make([]Label, len(labels))
	copy(out, labels)
	return out
}

// appendSeries appends the newest point of s to out. A series with no parseable
// value appends nothing: unparseable and missing values yield absent samples,
// never zeros (ADR-0007). Passing no labels yields a sample with none.
func appendSeries(out []Sample, name string, s Series, labels ...Label) []Sample {
	v, ok := s.Latest()
	if !ok {
		return out
	}
	return append(out, Sample{Name: name, Labels: copyLabels(labels), Value: v})
}

// appendNum appends n to out when it parsed. An unset Num appends nothing —
// absent, never zero (ADR-0007). A Num that parsed as 0 is real data and is
// emitted.
func appendNum(out []Sample, name string, n Num, labels ...Label) []Sample {
	if !n.Set {
		return out
	}
	return append(out, Sample{Name: name, Labels: copyLabels(labels), Value: n.Val})
}

// appendBool appends b to out as 1 or 0 when the cluster reported it. An unset
// Bool appends nothing — absent, never zero (ADR-0007). A reported false is
// emitted as 0 rather than dropped: "this is switched off" is real information,
// and only silence about the flag is absence.
func appendBool(out []Sample, name string, b Bool, labels ...Label) []Sample {
	if !b.Set {
		return out
	}
	v := 0.0
	if b.Val {
		v = 1
	}
	return append(out, Sample{Name: name, Labels: copyLabels(labels), Value: v})
}
