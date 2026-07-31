package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fjacquet/obs_exporter/internal/config"
)

// clustersNamed builds a minimal Config carrying clusters with only the names
// set, enough to exercise selectCluster without touching config.Load.
func clustersNamed(names ...string) *config.Config {
	cfg := &config.Config{}
	for _, n := range names {
		cfg.Clusters = append(cfg.Clusters, config.Cluster{Name: n})
	}
	return cfg
}

func TestSelectClusterEmptyNamePicksFirst(t *testing.T) {
	cfg := clustersNamed("alpha", "beta")
	got, err := selectCluster(cfg, "")
	if err != nil {
		t.Fatalf("selectCluster: %v", err)
	}
	if got.Name != "alpha" {
		t.Errorf("selectCluster(\"\") = %q, want the first configured cluster %q", got.Name, "alpha")
	}
}

func TestSelectClusterMatchesByName(t *testing.T) {
	cfg := clustersNamed("alpha", "beta")
	got, err := selectCluster(cfg, "beta")
	if err != nil {
		t.Fatalf("selectCluster: %v", err)
	}
	if got.Name != "beta" {
		t.Errorf("selectCluster(%q) = %q, want %q", "beta", got.Name, "beta")
	}
}

func TestSelectClusterUnknownNameErrors(t *testing.T) {
	cfg := clustersNamed("alpha", "beta")
	if _, err := selectCluster(cfg, "not-there"); err == nil {
		t.Fatal("selectCluster with an unknown name returned no error")
	}
}

func TestSelectClusterEmptyNameOnNoClustersErrors(t *testing.T) {
	cfg := &config.Config{}
	if _, err := selectCluster(cfg, ""); err == nil {
		t.Fatal("selectCluster on a config with no clusters returned no error")
	}
}

func TestCountRowsSumsAllSeries(t *testing.T) {
	raw := json.RawMessage(`{"Series":[
		{"Columns":["a"],"Values":[["1"],["2"]]},
		{"Columns":["a"],"Values":[["3"]]}
	]}`)
	if got := countRows(raw); got != 3 {
		t.Errorf("countRows = %d, want 3", got)
	}
}

func TestCountRowsOnUnparseableInputReturnsZero(t *testing.T) {
	if got := countRows([]byte("not json")); got != 0 {
		t.Errorf("countRows on garbage = %d, want 0", got)
	}
}

func TestFluxCaptureCmdRegistersOutputAndClusterFlags(t *testing.T) {
	cmd := fluxCaptureCmd()
	if cmd.Use != "flux-capture" {
		t.Errorf("Use = %q, want %q", cmd.Use, "flux-capture")
	}
	for _, name := range []string{"config", "cluster", "out", "bucket", "measurement"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flux-capture is missing --%s", name)
		}
	}
	if !strings.Contains(cmd.Long, "gitignored") {
		t.Error("flux-capture's Long help does not mention that the default output directory is gitignored")
	}
}
