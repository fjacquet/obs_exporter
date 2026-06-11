package ecs

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"
)

// PromCollector is an unchecked Prometheus collector: Describe emits nothing so the
// metric-name set can vary per snapshot. Collect reads the latest snapshot.
type PromCollector struct {
	store *SnapshotStore
}

// NewPromCollector wraps the snapshot store as a prometheus.Collector.
func NewPromCollector(store *SnapshotStore) *PromCollector { return &PromCollector{store: store} }

// Describe sends nothing (unchecked collector).
func (p *PromCollector) Describe(chan<- *prometheus.Desc) {}

// Collect turns every snapshot sample into a gauge metric.
//
// As an unchecked collector, client_golang does not enforce a consistent label-key
// set per metric name during Gather, so we enforce it here: the first label-key set
// seen for a name within a scrape defines that metric's schema, and later samples
// whose keys disagree are dropped to keep the exported series shape stable.
func (p *PromCollector) Collect(ch chan<- prometheus.Metric) {
	snap := p.store.Load()
	schema := map[string][]string{}
	for _, cluster := range snap.Clusters {
		for _, s := range cluster.Samples {
			keys := make([]string, len(s.Labels))
			vals := make([]string, len(s.Labels))
			for i, l := range s.Labels {
				keys[i], vals[i] = l.Key, l.Value
			}
			if want, ok := schema[s.Name]; ok {
				if !slices.Equal(want, keys) {
					continue // label-key drift for an already-seen metric name
				}
			} else {
				schema[s.Name] = keys
			}
			desc := prometheus.NewDesc(s.Name, "ECS metric "+s.Name, keys, nil)
			m, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, s.Value, vals...)
			if err != nil {
				continue // skip inconsistent label sets rather than panic
			}
			ch <- m
		}
	}
}
