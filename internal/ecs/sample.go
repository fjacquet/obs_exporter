// Package ecs holds the ECS metric model, snapshot store, modular resource
// collectors, and the Prometheus + OTLP export paths.
package ecs

// Label is a single metric label key/value.
type Label struct {
	Key   string
	Value string
}

// Sample is one metric data point: a name, an ordered label set, and a value.
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
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
func (s Sample) WithCluster(name string) Sample {
	labels := make([]Label, 0, len(s.Labels)+1)
	labels = append(labels, Label{Key: "cluster", Value: name})
	labels = append(labels, s.Labels...)
	return Sample{Name: s.Name, Labels: labels, Value: s.Value}
}

// appendSeries appends the newest point of s to out. A series with no parseable
// value appends nothing: unparseable and missing values yield absent samples,
// never zeros (ADR-0007). Passing no labels yields a sample with none.
func appendSeries(out []Sample, name string, s Series, labels ...Label) []Sample {
	v, ok := s.Latest()
	if !ok {
		return out
	}
	return append(out, Sample{Name: name, Labels: labels, Value: v})
}

// appendNum appends n to out when it parsed. An unset Num appends nothing —
// absent, never zero (ADR-0007). A Num that parsed as 0 is real data and is
// emitted.
func appendNum(out []Sample, name string, n Num, labels ...Label) []Sample {
	if !n.Set {
		return out
	}
	return append(out, Sample{Name: name, Labels: labels, Value: n.Val})
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
	return append(out, Sample{Name: name, Labels: labels, Value: v})
}
