package ecs

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestOTLPExporterObservesSnapshot(t *testing.T) {
	store := NewSnapshotStore()
	col := NewCollector(testTargets(t), store, time.Minute, 10*time.Second)

	reader := sdkmetric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	col.PostCycle = func() {
		if err := exp.EnsureInstruments(); err != nil {
			t.Errorf("EnsureInstruments: %v", err)
		}
	}
	col.CollectOnce(context.Background())

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	got := map[string]float64{}
	// bySeries keys data points by name plus their full attribute set, so the
	// consolidated families (state/type/op/direction/kind) can be asserted without
	// their series colliding under the bare metric name.
	bySeries := map[string]float64{}
	var clusterAttr bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			g, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				continue
			}
			for _, dp := range g.DataPoints {
				got[m.Name] = dp.Value
				bySeries[m.Name+"{"+dp.Attributes.Encoded(attribute.DefaultEncoder())+"}"] = dp.Value
				if v, ok := dp.Attributes.Value(attribute.Key("cluster")); ok && v.AsString() == "test-cluster" {
					clusterAttr = true
				}
			}
		}
	}
	if got["ecs_up"] != 1 {
		t.Errorf("ecs_up = %v, want 1", got["ecs_up"])
	}
	if got["ecs_cluster_nodes_installed"] != 4 {
		t.Errorf("ecs_cluster_nodes_installed = %v, want 4", got["ecs_cluster_nodes_installed"])
	}
	// The consolidated families must survive the OTLP path with their
	// distinguishing attribute intact, not just their name.
	for key, want := range map[string]float64{
		"ecs_cluster_nodes{cluster=test-cluster,state=good}":                  4,
		"ecs_cluster_disk_space_bytes{cluster=test-cluster,type=reserved}":    1500,
		"ecs_cluster_gc_bytes{cluster=test-cluster,scope=user,state=pending}": 900,
	} {
		if bySeries[key] != want {
			t.Errorf("%s = %v, want %v", key, bySeries[key], want)
		}
	}
	if got["ecs_cluster_replication_rpo_lag_seconds"] != 7200 {
		t.Errorf("ecs_cluster_replication_rpo_lag_seconds = %v, want 7200", got["ecs_cluster_replication_rpo_lag_seconds"])
	}
	if got["ecs_cluster_recovery_bad_chunks_bytes"] != 10992 {
		t.Errorf("ecs_cluster_recovery_bad_chunks_bytes = %v, want 10992", got["ecs_cluster_recovery_bad_chunks_bytes"])
	}
	if got["ecs_cluster_ec_coded_ratio_percent"] != 98.3 {
		t.Errorf("ecs_cluster_ec_coded_ratio_percent = %v, want 98.3", got["ecs_cluster_ec_coded_ratio_percent"])
	}
	// The gc/allocation families are labelled (scope, purpose) with multiple
	// series each; got is keyed by metric name alone, so a labelled series
	// would collide here. Those families are covered by the Prometheus gather
	// test instead, which counts series per family.
	if !clusterAttr {
		t.Error("cluster attribute missing from OTLP data points")
	}

	// Second cycle must not re-register instruments (idempotency).
	col.CollectOnce(context.Background())
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
}

func TestOTLPExporterRegistersCounters(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{Name: "ecs_node_requests_total", Labels: []Label{{"node", "n1"}}, Value: 5, Type: Counter},
			{Name: "ecs_node_cpu_utilization_percent", Labels: []Label{{"node", "n1"}}, Value: 12},
		},
	}}})

	reader := sdkmetric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	if err := exp.EnsureInstruments(); err != nil {
		t.Fatal(err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[float64]:
				if d.IsMonotonic {
					kinds[m.Name] = "counter"
				}
			case metricdata.Gauge[float64]:
				kinds[m.Name] = "gauge"
			}
		}
	}
	if kinds["ecs_node_requests_total"] != "counter" {
		t.Errorf("requests_total registered as %q, want counter", kinds["ecs_node_requests_total"])
	}
	if kinds["ecs_node_cpu_utilization_percent"] != "gauge" {
		t.Errorf("cpu_utilization registered as %q, want gauge", kinds["ecs_node_cpu_utilization_percent"])
	}
}

// TestOTLPExporterCarriesLeAttribute is the OTLP mirror of
// TestPromCollectorAcceptsLeAsVariableLabel: a histogram bucket sample's le
// label must survive as an ordinary attribute on the counter data point.
func TestOTLPExporterCarriesLeAttribute(t *testing.T) {
	store := NewSnapshotStore()
	store.Store(&Snapshot{Clusters: []*ClusterSnapshot{{
		Cluster: "c1",
		Samples: []Sample{
			{
				Name:   "ecs_node_transaction_latency_milliseconds_bucket",
				Labels: []Label{{"node", "n1"}, {"op", "read"}, {"le", "+Inf"}},
				Value:  5, Type: Counter,
			},
		},
	}}})

	reader := sdkmetric.NewManualReader()
	exp := newOTLPExporter(reader, store, "test")
	if err := exp.EnsureInstruments(); err != nil {
		t.Fatal(err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "ecs_node_transaction_latency_milliseconds_bucket" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[float64])
			if !ok {
				t.Fatalf("bucket registered as %T, want Sum[float64]", m.Data)
			}
			for _, dp := range sum.DataPoints {
				if v, ok := dp.Attributes.Value(attribute.Key("le")); ok && v.AsString() == "+Inf" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("le attribute missing from the bucket data point")
	}
}
