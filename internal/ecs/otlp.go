package ecs

import (
	"context"
	"sync"
	"time"

	"github.com/fjacquet/obs_exporter/internal/config"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OTLPExporter pushes the snapshot's metrics via OTLP using asynchronous observable
// gauges. The periodic reader drives collection: on each push every registered
// instrument's callback reads the latest snapshot and observes its samples. Metric
// names can appear as clusters come online, so EnsureInstruments runs after every
// collection cycle (idempotent).
type OTLPExporter struct {
	provider *sdkmetric.MeterProvider
	meter    metric.Meter
	store    *SnapshotStore

	mu         sync.Mutex
	registered map[string]struct{}
}

// NewOTLPExporter creates an exporter that pushes metrics to an OTLP gRPC endpoint.
func NewOTLPExporter(ctx context.Context, oc config.OTLP, store *SnapshotStore, serviceVersion string) (*OTLPExporter, error) {
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(oc.Endpoint)}
	if oc.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	interval := oc.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	reader := sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(interval))
	return newOTLPExporter(reader, store, serviceVersion), nil
}

// newOTLPExporter builds the meter provider from a reader. Separated so tests can
// inject a ManualReader.
func newOTLPExporter(reader sdkmetric.Reader, store *SnapshotStore, serviceVersion string) *OTLPExporter {
	res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("obs_exporter"),
		semconv.ServiceVersion(serviceVersion),
	))
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	return &OTLPExporter{
		provider:   provider,
		meter:      provider.Meter("obs_exporter"),
		store:      store,
		registered: make(map[string]struct{}),
	}
}

// EnsureInstruments registers an observable gauge for every metric name in the
// current snapshot that does not already have one. Idempotent; runs after every
// collection cycle via Collector.PostCycle.
func (e *OTLPExporter) EnsureInstruments() error {
	snap := e.store.Load()
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, name := range snap.MetricNames() {
		if _, ok := e.registered[name]; ok {
			continue
		}
		metricName := name
		_, err := e.meter.Float64ObservableGauge(metricName,
			metric.WithFloat64Callback(func(_ context.Context, obs metric.Float64Observer) error {
				for _, s := range e.store.Load().SamplesByName(metricName) {
					obs.Observe(s.Value, metric.WithAttributes(attrsFor(s.Labels)...))
				}
				return nil
			}),
		)
		if err != nil {
			return err
		}
		e.registered[metricName] = struct{}{}
	}
	return nil
}

// Shutdown flushes and stops the meter provider.
func (e *OTLPExporter) Shutdown(ctx context.Context) error {
	return e.provider.Shutdown(ctx)
}

func attrsFor(labels []Label) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, len(labels))
	for i, l := range labels {
		attrs[i] = attribute.String(l.Key, l.Value)
	}
	return attrs
}
