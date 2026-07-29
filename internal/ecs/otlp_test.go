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
	var clusterAttr bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			g, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				continue
			}
			for _, dp := range g.DataPoints {
				got[m.Name] = dp.Value
				if v, ok := dp.Attributes.Value(attribute.Key("cluster")); ok && v.AsString() == "test-cluster" {
					clusterAttr = true
				}
			}
		}
	}
	if got["ecs_up"] != 1 {
		t.Errorf("ecs_up = %v, want 1", got["ecs_up"])
	}
	if got["ecs_cluster_good_nodes"] != 4 {
		t.Errorf("ecs_cluster_good_nodes = %v, want 4", got["ecs_cluster_good_nodes"])
	}
	// New cluster capacity + RPO samples must also reach the OTLP export path.
	if got["ecs_cluster_disk_space_reserved_bytes"] != 1500 {
		t.Errorf("ecs_cluster_disk_space_reserved_bytes = %v, want 1500", got["ecs_cluster_disk_space_reserved_bytes"])
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
