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
