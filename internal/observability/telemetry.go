package observability

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/golosoman/codex-reset-watcher/internal/domain"
	"github.com/golosoman/codex-reset-watcher/internal/monitor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexport "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type Telemetry struct {
	Handler                                                      http.Handler
	Tracer                                                       trace.Tracer
	meter                                                        *sdkmetric.MeterProvider
	traces                                                       *sdktrace.TracerProvider
	checks, requests, sourceErrors, items, events, notifications metric.Int64Counter
	duration, sourceDuration                                     metric.Float64Histogram
	lastSuccess, available                                       metric.Int64ObservableGauge
	mu                                                           sync.Mutex
	success, availability                                        map[string]int64
}

func New(ctx context.Context, endpoint string) (*Telemetry, error) {
	registry := prometheus.NewRegistry()
	exporter, err := promexport.New(promexport.WithRegisterer(registry))
	if err != nil {
		return nil, err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithResource(resource.NewSchemaless(attribute.String("service.name", "codex-reset-watcher"))))
	t := &Telemetry{Handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), meter: provider, Tracer: noop.NewTracerProvider().Tracer("watcher"), success: map[string]int64{}, availability: map[string]int64{}}
	meter := provider.Meter("watcher")
	t.checks, err = meter.Int64Counter("watcher_checks")
	if err != nil {
		return nil, err
	}
	t.requests, err = meter.Int64Counter("watcher_source_requests")
	if err != nil {
		return nil, err
	}
	t.sourceErrors, err = meter.Int64Counter("watcher_source_errors")
	if err != nil {
		return nil, err
	}
	t.items, err = meter.Int64Counter("watcher_items_received")
	if err != nil {
		return nil, err
	}
	t.events, err = meter.Int64Counter("watcher_events_detected")
	if err != nil {
		return nil, err
	}
	t.notifications, err = meter.Int64Counter("watcher_notifications")
	if err != nil {
		return nil, err
	}
	t.duration, err = meter.Float64Histogram("watcher_check_duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.1, 1, 5, 10, 30, 60, 120))
	if err != nil {
		return nil, err
	}
	t.lastSuccess, err = meter.Int64ObservableGauge("watcher_last_success_timestamp")
	if err != nil {
		return nil, err
	}
	t.sourceDuration, err = meter.Float64Histogram("watcher_source_duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.1, 1, 5, 10, 30))
	if err != nil {
		return nil, err
	}
	t.available, err = meter.Int64ObservableGauge("watcher_source_available")
	if err != nil {
		return nil, err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		t.mu.Lock()
		defer t.mu.Unlock()
		for name, stamp := range t.success {
			observer.ObserveInt64(t.lastSuccess, stamp, metric.WithAttributes(attribute.String("source", name)))
		}
		for name, status := range t.availability {
			observer.ObserveInt64(t.available, status, metric.WithAttributes(attribute.String("source", name)))
		}
		return nil
	}, t.lastSuccess, t.available)
	if err != nil {
		return nil, err
	}
	if endpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithTimeout(3*time.Second))
		if err != nil {
			return nil, err
		}
		t.traces = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(256)), sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "codex-reset-watcher"))))
		t.Tracer = t.traces.Tracer("watcher")
	}
	return t, nil
}
func (t *Telemetry) Source(ctx context.Context, info domain.SourceInfo, duration time.Duration, received, _, events int, err error) {
	labels := metric.WithAttributes(attribute.String("source", info.Name))
	t.sourceDuration.Record(ctx, duration.Seconds(), labels)
	t.requests.Add(ctx, 1, labels)
	t.items.Add(ctx, int64(received), labels)
	t.events.Add(ctx, int64(events), labels)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.sourceErrors.Add(ctx, 1, labels)
		t.availability[info.Name] = 0
	} else {
		t.success[info.Name] = time.Now().UTC().Unix()
		t.availability[info.Name] = 1
	}
}
func (t *Telemetry) Cycle(ctx context.Context, r monitor.Run) {
	t.checks.Add(ctx, 1)
	t.duration.Record(ctx, r.Duration.Seconds())
}
func (t *Telemetry) Delivery(ctx context.Context, state string) {
	t.notifications.Add(ctx, 1, metric.WithAttributes(attribute.String("status", state)))
}
func (t *Telemetry) Close(ctx context.Context) error {
	if t.traces != nil {
		if err := t.traces.Shutdown(ctx); err != nil {
			return err
		}
	}
	return t.meter.Shutdown(ctx)
}
