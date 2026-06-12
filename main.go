// Command obs_exporter is a Prometheus + OTLP exporter for Dell EMC ECS /
// ObjectScale object-storage clusters.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fjacquet/obs_exporter/internal/config"
	"github.com/fjacquet/obs_exporter/internal/ecs"
	"github.com/fjacquet/obs_exporter/internal/ecsclient"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	var cfgPath string
	var once, debug, trace bool
	root := &cobra.Command{
		Use:     "obs_exporter",
		Version: version,
		RunE: func(_ *cobra.Command, _ []string) error {
			return run(cfgPath, once, debug, trace)
		},
	}
	root.Flags().StringVar(&cfgPath, "config", "config.yaml", "path to config file")
	root.Flags().BoolVar(&once, "once", false, "run a single collection cycle and exit")
	root.Flags().BoolVar(&debug, "debug", false, "verbose logging")
	root.Flags().BoolVar(&trace, "trace", false, "log every management API response body (live-cluster payload validation; very verbose)")
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func run(cfgPath string, once, debug, trace bool) error {
	if debug {
		log.SetLevel(log.DebugLevel)
	}
	// Load .env (if present) before interpolation so the `cp .env.example .env`
	// quickstart works for bare-metal runs too; real env vars always win.
	config.LoadDotEnv(cfgPath)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	store := ecs.NewSnapshotStore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if once {
		targets := buildTargets(cfg, trace)
		col := ecs.NewCollector(targets, store, cfg.Collection.Interval, cfg.Collection.Timeout)
		log.Info("running single collection cycle")
		snap := col.CollectOnce(ctx)
		closeTargets(targets)
		if debug {
			dumpSamples(snap)
		}
		for _, cs := range snap.Clusters {
			log.WithFields(log.Fields{"cluster": cs.Cluster, "ok": cs.OK, "samples": len(cs.Samples)}).
				Info("collection done")
		}
		return nil
	}

	// Optional OTLP push path: shares the snapshot store with /metrics.
	var otlp *ecs.OTLPExporter
	postCycle := func() {}
	if cfg.OTLP.Endpoint != "" {
		otlp, err = ecs.NewOTLPExporter(ctx, cfg.OTLP, store, version)
		if err != nil {
			return err
		}
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = otlp.Shutdown(sctx)
		}()
		postCycle = func() {
			if err := otlp.EnsureInstruments(); err != nil {
				log.WithError(err).Warn("OTLP instrument registration failed")
			}
		}
		log.WithField("endpoint", cfg.OTLP.Endpoint).Info("OTLP metric export enabled")
	}

	// runner owns the live collection loop and its clients so config reloads can
	// rebuild and swap them in place. The SnapshotStore is shared and never
	// replaced, so /metrics and /health keep serving across a swap.
	runner := newCollectorRunner(store, postCycle, trace)
	defer runner.stop()

	if w, err := config.NewWatcher(cfgPath); err == nil {
		defer func() { _ = w.Close() }()
		go func() {
			serverCfg := cfg.Server
			for {
				select {
				case <-ctx.Done():
					return
				case newCfg, ok := <-w.Updates():
					if !ok {
						return
					}
					runner.apply(ctx, newCfg)
					entry := log.WithField("clusters", len(newCfg.Clusters))
					if newCfg.Server != serverCfg {
						entry.Warn("config reloaded and applied; server host/port/uri changed — restart to apply those")
					} else {
						entry.Info("config reloaded and applied")
					}
				}
			}
		}()
	} else {
		log.WithError(err).Warn("config watcher disabled (failed to start)")
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "obs_exporter_build_info",
		Help: "A metric with a constant '1' value labeled by the exporter version and Go version",
	}, []string{"version", "goversion"})
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)

	reg := prometheus.NewRegistry()
	reg.MustRegister(buildInfo)
	reg.MustRegister(ecs.NewPromCollector(store))

	mux := http.NewServeMux()
	mux.Handle(cfg.Server.URI, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		healthHandler(w, store)
	})

	srv := &http.Server{
		Addr:              cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	// Serve before the first collection cycle: login plus the first poll of every
	// cluster can take longer than a scrape timeout, and a blocked /metrics looks
	// like a dead exporter. Scrapes before the first snapshot just return the
	// build-info metric and an empty snapshot.
	errCh := make(chan error, 1)
	go func() {
		log.WithField("addr", srv.Addr).Info("serving metrics")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	log.Info("running initial collection cycle")
	runner.apply(ctx, cfg)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down: logging out of all clusters")
		return <-errCh
	}
}

// collectorRunner owns the live collection loop and its ECS clients so a config
// reload can rebuild and swap them atomically. apply() is serialized by the single
// watcher goroutine (plus the one startup call), so it needs no caller-side locking;
// the mutex only guards the swap against a concurrent stop() at shutdown.
type collectorRunner struct {
	store     *ecs.SnapshotStore
	postCycle func()
	trace     bool
	mu        sync.Mutex
	targets   []ecs.Target
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func newCollectorRunner(store *ecs.SnapshotStore, postCycle func(), trace bool) *collectorRunner {
	return &collectorRunner{store: store, postCycle: postCycle, trace: trace}
}

// apply stops any running loop, then builds clients + a collector from cfg, runs one
// immediate cycle (so new clusters appear without waiting a full interval), and
// starts the background loop.
func (r *collectorRunner) apply(parent context.Context, cfg *config.Config) {
	r.shutdownCurrent()

	targets := buildTargets(cfg, r.trace)
	col := ecs.NewCollector(targets, r.store, cfg.Collection.Interval, cfg.Collection.Timeout)
	col.PostCycle = r.postCycle
	loopCtx, cancel := context.WithCancel(parent)

	r.mu.Lock()
	r.targets, r.cancel = targets, cancel
	r.mu.Unlock()

	col.CollectOnce(loopCtx)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		col.Run(loopCtx)
	}()
}

// shutdownCurrent cancels the running loop, waits for it to exit, and logs its
// clients out. Safe to call when nothing is running.
func (r *collectorRunner) shutdownCurrent() {
	r.mu.Lock()
	cancel, targets := r.cancel, r.targets
	r.cancel, r.targets = nil, nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
	closeTargets(targets)
}

func (r *collectorRunner) stop() { r.shutdownCurrent() }

// buildTargets constructs one ECS client (plus its collector set) per configured
// cluster.
func buildTargets(cfg *config.Config, trace bool) []ecs.Target {
	targets := make([]ecs.Target, 0, len(cfg.Clusters))
	for _, cl := range cfg.Clusters {
		client := ecsclient.NewClusterClient(ecsclient.Config{
			Name: cl.Name, BaseURL: cl.BaseURL(), Username: cl.Username,
			Password: cl.Password, InsecureSkipVerify: cl.InsecureSkipVerify,
			Trace: trace,
		})
		targets = append(targets, ecs.Target{Client: client, Collectors: ecs.Registry(cl)})
	}
	return targets
}

// dumpSamples prints every collected sample in Prometheus exposition style,
// sorted, so a `--once --debug` run against a live cluster can be diffed against
// docs/metrics.md to spot silently-absent metrics.
func dumpSamples(snap *ecs.Snapshot) {
	var lines []string
	for _, cs := range snap.Clusters {
		for _, s := range cs.Samples {
			parts := make([]string, 0, len(s.Labels))
			for _, l := range s.Labels {
				parts = append(parts, fmt.Sprintf("%s=%q", l.Key, l.Value))
			}
			lines = append(lines, fmt.Sprintf("%s{%s} %v", s.Name, strings.Join(parts, ","), s.Value))
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
}

// closeTargets logs every client out so ECS session tokens are released (ECS caps
// tokens per user; leaking them eventually locks the monitoring account out).
func closeTargets(targets []ecs.Target) {
	for _, t := range targets {
		if err := t.Client.Close(); err != nil {
			log.WithFields(log.Fields{"cluster": t.Client.Name(), "err": err}).Debug("logout failed")
		}
	}
}

func healthHandler(w http.ResponseWriter, store *ecs.SnapshotStore) {
	snap := store.Load()
	type clusterHealth struct {
		Cluster    string `json:"cluster"`
		OK         bool   `json:"ok"`
		LastScrape string `json:"last_scrape"`
		Err        string `json:"err,omitempty"`
	}
	out := struct {
		BuiltAt  string          `json:"built_at"`
		Clusters []clusterHealth `json:"clusters"`
	}{BuiltAt: snap.BuiltAt.Format(time.RFC3339)}
	healthy := len(snap.Clusters) > 0
	for _, c := range snap.Clusters {
		out.Clusters = append(out.Clusters, clusterHealth{c.Cluster, c.OK, c.LastScrape.Format(time.RFC3339), c.Err})
		if !c.OK {
			healthy = false
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(out)
}
