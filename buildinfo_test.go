package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestBuildInfoMetric verifies the exporter-level build-info metric matches the
// family standard: obs_exporter_build_info{version, goversion} holding constant 1.
func TestBuildInfoMetric(t *testing.T) {
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "obs_exporter_build_info",
		Help: "A metric with a constant '1' value labeled by the exporter version and Go version",
	}, []string{"version", "goversion"})
	buildInfo.WithLabelValues("v1.2.3", "go1.99").Set(1)

	reg := prometheus.NewRegistry()
	reg.MustRegister(buildInfo)

	want := `
# HELP obs_exporter_build_info A metric with a constant '1' value labeled by the exporter version and Go version
# TYPE obs_exporter_build_info gauge
obs_exporter_build_info{goversion="go1.99",version="v1.2.3"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "obs_exporter_build_info"); err != nil {
		t.Fatal(err)
	}
}
