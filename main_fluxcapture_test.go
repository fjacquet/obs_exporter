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

// TestSortedScriptKeysIsDeterministic pins flux-capture's write/print/summary
// order: sortedScriptKeys must return the same sorted order regardless of the
// map's internal iteration order, which Go deliberately randomizes. Without
// this, the files written, the console output, and summary.json's entries
// would vary between otherwise-identical runs, making two captures needlessly
// hard to diff.
func TestSortedScriptKeysIsDeterministic(t *testing.T) {
	scripts := map[string]string{
		"monitoring_vdc/cq_performance_transaction":                 "q1",
		"monitoring_op/cpu":                                         "q2",
		"monitoring_main/statDataHead_performance_internal_latency": "q3",
		"monitoring_op/mem":                                         "q4",
		"monitoring_op/dtquery_dt_status":                           "q5",
	}
	want := []string{
		"monitoring_main/statDataHead_performance_internal_latency",
		"monitoring_op/cpu",
		"monitoring_op/dtquery_dt_status",
		"monitoring_op/mem",
		"monitoring_vdc/cq_performance_transaction",
	}
	// Run several times: map iteration order is randomized per-process, not
	// deterministic across a single run, so one call proves little.
	for i := 0; i < 5; i++ {
		got := sortedScriptKeys(scripts)
		if len(got) != len(want) {
			t.Fatalf("run %d: len(sortedScriptKeys) = %d, want %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d: sortedScriptKeys()[%d] = %q, want %q (full: %v)", i, j, got[j], want[j], got)
			}
		}
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

// TestFluxCaptureRejectsOneOfBucketOrMeasurement guards against a silent
// whole-table capture when an operator sets only one of the two probe flags:
// --bucket and --measurement are documented as used together
// (docs/operate/flux-validation.md), and giving only one used to fall through
// to capturing all ten table queries with no error and no file named after
// the flag the operator did type.
func TestFluxCaptureRejectsOneOfBucketOrMeasurement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bucket      string
		measurement string
	}{
		{name: "measurement only", measurement: "diskio"},
		{name: "bucket only", bucket: "monitoring_op"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := fluxCaptureCmd()
			cmd.SetArgs([]string{
				"--config", "does-not-exist.yaml",
				"--bucket", tc.bucket,
				"--measurement", tc.measurement,
			})
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			if err == nil {
				t.Fatal("Execute with only one of --bucket/--measurement set returned no error")
			}
			if !strings.Contains(err.Error(), "--bucket") || !strings.Contains(err.Error(), "--measurement") {
				t.Errorf("error %q does not name both flags", err.Error())
			}
		})
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
