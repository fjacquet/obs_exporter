package ecs

import (
	"context"
	"fmt"
	"time"

	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Target pairs a cluster client with its resource collectors (which depend on
// per-cluster feature flags).
type Target struct {
	Client     ecsclient.Client
	Collectors []ResourceCollector
}

// Collector runs the background loop: every interval it polls all clusters in
// parallel and publishes a fresh Snapshot. One cluster's failure never blocks others.
type Collector struct {
	targets  []Target
	store    *SnapshotStore
	interval time.Duration
	timeout  time.Duration
	// PostCycle, when set, runs after every published snapshot (the OTLP exporter
	// uses it to register instruments for newly appearing metric names).
	PostCycle func()
}

// NewCollector wires the loop.
func NewCollector(targets []Target, store *SnapshotStore, interval, timeout time.Duration) *Collector {
	return &Collector{targets: targets, store: store, interval: interval, timeout: timeout}
}

// CollectOnce runs a single cycle, stores, and returns the snapshot.
func (c *Collector) CollectOnce(ctx context.Context) *Snapshot {
	snap := c.collectAll(ctx)
	c.store.Store(snap)
	if c.PostCycle != nil {
		c.PostCycle()
	}
	return snap
}

// Run loops until ctx is cancelled (assumes CollectOnce already primed the store).
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.CollectOnce(ctx)
		}
	}
}

func (c *Collector) collectAll(ctx context.Context) *Snapshot {
	results := make([]*ClusterSnapshot, len(c.targets))
	g, gctx := errgroup.WithContext(ctx)
	for i, target := range c.targets {
		g.Go(func() error {
			results[i] = c.collectCluster(gctx, target)
			return nil // graceful degradation
		})
	}
	_ = g.Wait()
	return &Snapshot{BuiltAt: time.Now(), Clusters: results}
}

func (c *Collector) collectCluster(ctx context.Context, target Target) *ClusterSnapshot {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	name := target.Client.Name()
	cs := &ClusterSnapshot{Cluster: name, LastScrape: time.Now(), OK: true}
	failures := 0
	var lastErr error
	domainSamples := 0
	for _, rc := range target.Collectors {
		samples, err := rc.Collect(ctx, target.Client)
		up := 1.0
		if err != nil {
			up = 0
			failures++
			lastErr = err
			log.WithFields(log.Fields{"cluster": name, "collector": rc.Name(), "err": err}).
				Warn("collector failed")
		}
		cs.Samples = append(cs.Samples, Sample{
			Name:   "ecs_collector_up",
			Labels: []Label{{Key: "collector", Value: rc.Name()}},
			Value:  up,
		}.WithCluster(name))
		for _, s := range samples {
			cs.Samples = append(cs.Samples, s.WithCluster(name))
			domainSamples++
		}
	}
	switch {
	case len(target.Collectors) > 0 && failures == len(target.Collectors):
		cs.OK = false
		cs.Err = fmt.Sprintf("all %d collectors failed: %v", len(target.Collectors), lastErr)
	case len(target.Collectors) > 0 && domainSamples == 0:
		cs.OK = false
		cs.Err = fmt.Sprintf("no domain samples collected (failures: %d/%d)", failures, len(target.Collectors))
	}
	up := 0.0
	if cs.OK {
		up = 1
	}
	cs.Samples = append(cs.Samples, Sample{Name: "ecs_up", Value: up}.WithCluster(name))
	return cs
}
