package ecs

import (
	"sort"
	"sync"
	"time"
)

// ClusterSnapshot is one cluster's result for a single collection cycle.
type ClusterSnapshot struct {
	Cluster    string
	LastScrape time.Time
	OK         bool   // true if at least one collector returned domain samples
	Err        string // top-level failure summary; empty when OK
	Samples    []Sample
}

// Snapshot is an immutable, point-in-time view across all clusters.
type Snapshot struct {
	BuiltAt  time.Time
	Clusters []*ClusterSnapshot
}

// MetricNames returns the sorted set of metric names present in the snapshot.
func (s *Snapshot) MetricNames() []string {
	set := map[string]struct{}{}
	for _, c := range s.Clusters {
		for _, smp := range c.Samples {
			set[smp.Name] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// SamplesByName returns every sample with the given metric name.
func (s *Snapshot) SamplesByName(name string) []Sample {
	var out []Sample
	for _, c := range s.Clusters {
		for _, smp := range c.Samples {
			if smp.Name == name {
				out = append(out, smp)
			}
		}
	}
	return out
}

// SnapshotStore holds the latest Snapshot behind an RWMutex pointer-swap.
type SnapshotStore struct {
	mu   sync.RWMutex
	snap *Snapshot
}

// NewSnapshotStore returns a store pre-populated with an empty snapshot so
// readers never see nil before the first collection cycle.
func NewSnapshotStore() *SnapshotStore {
	return &SnapshotStore{snap: &Snapshot{}}
}

// Store atomically swaps in a new snapshot.
func (s *SnapshotStore) Store(snap *Snapshot) {
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
}

// Load returns the current snapshot (never nil).
func (s *SnapshotStore) Load() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}
