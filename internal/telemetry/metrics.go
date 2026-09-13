// Package telemetry defines low-cardinality pipeline metrics. Entity IDs,
// tenant IDs and endpoint IDs intentionally never appear as metric labels.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	prometheusexporter "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const meterName = "github.com/vendor-status-monitoring/vendor-status-monitoring"

type Metrics struct {
	provider *sdkmetric.MeterProvider
	handler  http.Handler

	polls                   otelmetric.Int64Counter
	pollDuration            otelmetric.Float64Histogram
	pollBytes               otelmetric.Int64Histogram
	reconcileRuns           otelmetric.Int64Counter
	reconcileEvents         otelmetric.Int64Counter
	outboxPublishes         otelmetric.Int64Counter
	outboxAge               otelmetric.Float64Histogram
	busDeliveries           otelmetric.Int64Counter
	busRedeliveries         otelmetric.Int64Counter
	notificationSends       otelmetric.Int64Counter
	notificationLatency     otelmetric.Float64Histogram
	rateLimitWait           otelmetric.Float64Histogram
	fanoutRuns              otelmetric.Int64Counter
	fanoutDeliveries        otelmetric.Int64Counter
	fanoutDuration          otelmetric.Float64Histogram
	eligibleLatency         otelmetric.Float64Histogram
	deliveryLaneConcurrency otelmetric.Int64Histogram
	deliveryLaneClaimed     otelmetric.Int64Histogram
	sourceFreshness         otelmetric.Float64Histogram
	schemaDrift             otelmetric.Int64Counter
}

func NewPrometheus(registry *prometheus.Registry) (*Metrics, error) {
	if registry == nil {
		return nil, errors.New("Prometheus registry is required")
	}
	exporter, err := prometheusexporter.New(prometheusexporter.WithRegisterer(registry))
	if err != nil {
		return nil, fmt.Errorf("create Prometheus exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	meter := provider.Meter(meterName)

	metrics := &Metrics{provider: provider, handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{})}
	if metrics.polls, err = meter.Int64Counter("statusmon.source.polls", otelmetric.WithDescription("Source poll attempts")); err != nil {
		return nil, err
	}
	if metrics.pollDuration, err = meter.Float64Histogram("statusmon.source.poll.duration", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.pollBytes, err = meter.Int64Histogram("statusmon.source.poll.bytes", otelmetric.WithUnit("By")); err != nil {
		return nil, err
	}
	if metrics.reconcileRuns, err = meter.Int64Counter("statusmon.reconcile.runs"); err != nil {
		return nil, err
	}
	if metrics.reconcileEvents, err = meter.Int64Counter("statusmon.reconcile.events"); err != nil {
		return nil, err
	}
	if metrics.outboxPublishes, err = meter.Int64Counter("statusmon.outbox.publishes"); err != nil {
		return nil, err
	}
	if metrics.outboxAge, err = meter.Float64Histogram("statusmon.outbox.age", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.busDeliveries, err = meter.Int64Counter("statusmon.bus.deliveries"); err != nil {
		return nil, err
	}
	if metrics.busRedeliveries, err = meter.Int64Counter("statusmon.bus.redeliveries"); err != nil {
		return nil, err
	}
	if metrics.notificationSends, err = meter.Int64Counter("statusmon.notification.sends"); err != nil {
		return nil, err
	}
	if metrics.notificationLatency, err = meter.Float64Histogram("statusmon.notification.latency", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.rateLimitWait, err = meter.Float64Histogram("statusmon.notification.rate_limit_wait", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.fanoutRuns, err = meter.Int64Counter("statusmon.fanout.runs"); err != nil {
		return nil, err
	}
	if metrics.fanoutDeliveries, err = meter.Int64Counter("statusmon.fanout.deliveries"); err != nil {
		return nil, err
	}
	if metrics.fanoutDuration, err = meter.Float64Histogram("statusmon.fanout.duration", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.eligibleLatency, err = meter.Float64Histogram("statusmon.delivery.eligible_first_attempt", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.deliveryLaneConcurrency, err = meter.Int64Histogram("statusmon.delivery.lane.concurrency"); err != nil {
		return nil, err
	}
	if metrics.deliveryLaneClaimed, err = meter.Int64Histogram("statusmon.delivery.lane.claimed"); err != nil {
		return nil, err
	}
	if metrics.sourceFreshness, err = meter.Float64Histogram("statusmon.source.freshness", otelmetric.WithUnit("s")); err != nil {
		return nil, err
	}
	if metrics.schemaDrift, err = meter.Int64Counter("statusmon.source.schema_drift"); err != nil {
		return nil, err
	}
	return metrics, nil
}

func (m *Metrics) RecordFanout(ctx context.Context, result string, deliveries int64, duration time.Duration) {
	if m == nil {
		return
	}
	options := otelmetric.WithAttributes(attribute.String("result", bounded(result, "unknown")))
	m.fanoutRuns.Add(ctx, 1, options)
	if deliveries > 0 {
		m.fanoutDeliveries.Add(ctx, deliveries, options)
	}
	if duration >= 0 {
		m.fanoutDuration.Record(ctx, duration.Seconds(), options)
	}
}

func (m *Metrics) RecordEligibleFirstAttempt(ctx context.Context, channel string, delay time.Duration) {
	if m == nil || delay < 0 {
		return
	}
	m.eligibleLatency.Record(ctx, delay.Seconds(), otelmetric.WithAttributes(attribute.String("channel", bounded(channel, "unknown"))))
}

func (m *Metrics) RecordDeliveryLane(ctx context.Context, lane string, concurrency, claimed int) {
	if m == nil || concurrency < 0 || claimed < 0 {
		return
	}
	options := otelmetric.WithAttributes(attribute.String("lane", bounded(lane, "unknown")))
	m.deliveryLaneConcurrency.Record(ctx, int64(concurrency), options)
	m.deliveryLaneClaimed.Record(ctx, int64(claimed), options)
}

func (m *Metrics) RecordSourceFreshness(ctx context.Context, engine string, freshness time.Duration) {
	if m == nil || freshness < 0 {
		return
	}
	m.sourceFreshness.Record(ctx, freshness.Seconds(), otelmetric.WithAttributes(attribute.String("engine", bounded(engine, "unknown"))))
}

func (m *Metrics) RecordSchemaDrift(ctx context.Context, engine, resource string) {
	if m == nil {
		return
	}
	m.schemaDrift.Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("engine", bounded(engine, "unknown")),
		attribute.String("resource", bounded(resource, "unknown")),
	))
}

func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return m.handler
}

func (m *Metrics) Shutdown(ctx context.Context) error {
	if m == nil || m.provider == nil {
		return nil
	}
	return m.provider.Shutdown(ctx)
}

func (m *Metrics) RecordPoll(ctx context.Context, engine, result string, duration time.Duration, responseBytes int64, notModified bool) {
	if m == nil {
		return
	}
	attributes := []attribute.KeyValue{
		attribute.String("engine", bounded(engine, "unknown")),
		attribute.String("result", bounded(result, "unknown")),
		attribute.Bool("not_modified", notModified),
	}
	options := otelmetric.WithAttributes(attributes...)
	m.polls.Add(ctx, 1, options)
	m.pollDuration.Record(ctx, duration.Seconds(), options)
	if responseBytes >= 0 {
		m.pollBytes.Record(ctx, responseBytes, options)
	}
}

func (m *Metrics) RecordReconcile(ctx context.Context, result string, eventCount int) {
	if m == nil {
		return
	}
	options := otelmetric.WithAttributes(attribute.String("result", bounded(result, "unknown")))
	m.reconcileRuns.Add(ctx, 1, options)
	if eventCount > 0 {
		m.reconcileEvents.Add(ctx, int64(eventCount), options)
	}
}

func (m *Metrics) RecordOutboxPublish(ctx context.Context, subjectClass, result string, age time.Duration) {
	if m == nil {
		return
	}
	options := otelmetric.WithAttributes(
		attribute.String("subject_class", bounded(subjectClass, "unknown")),
		attribute.String("result", bounded(result, "unknown")),
	)
	m.outboxPublishes.Add(ctx, 1, options)
	if age >= 0 {
		m.outboxAge.Record(ctx, age.Seconds(), options)
	}
}

func (m *Metrics) RecordBusDelivery(ctx context.Context, consumerClass, result string, deliveryCount uint64) {
	if m == nil {
		return
	}
	options := otelmetric.WithAttributes(
		attribute.String("consumer_class", bounded(consumerClass, "unknown")),
		attribute.String("result", bounded(result, "unknown")),
	)
	m.busDeliveries.Add(ctx, 1, options)
	if deliveryCount > 1 {
		m.busRedeliveries.Add(ctx, 1, options)
	}
}

func (m *Metrics) RecordNotification(ctx context.Context, channel, result string, duration, retryAfter time.Duration) {
	if m == nil {
		return
	}
	options := otelmetric.WithAttributes(
		attribute.String("channel", bounded(channel, "unknown")),
		attribute.String("result", bounded(result, "unknown")),
	)
	m.notificationSends.Add(ctx, 1, options)
	m.notificationLatency.Record(ctx, duration.Seconds(), options)
	if retryAfter > 0 {
		m.rateLimitWait.Record(ctx, retryAfter.Seconds(), options)
	}
}

// bounded limits accidental cardinality explosions from dynamic values. Call
// sites should still pass enums/classes rather than identifiers.
func bounded(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 64 {
		return "other"
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			continue
		}
		return "other"
	}
	return value
}
