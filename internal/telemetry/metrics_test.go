package telemetry

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPrometheusMetricsAreExposedWithBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewPrometheus(registry)
	if err != nil {
		t.Fatalf("NewPrometheus() error = %v", err)
	}
	t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })

	ctx := context.Background()
	metrics.RecordPoll(ctx, "statuspage", "success", 125*time.Millisecond, 4096, true)
	metrics.RecordReconcile(ctx, "changed", 2)
	metrics.RecordOutboxPublish(ctx, "normal", "success", 2*time.Second)
	metrics.RecordBusDelivery(ctx, "fanout", "success", 2)
	metrics.RecordNotification(ctx, "slack", "rate_limited", 50*time.Millisecond, 3*time.Second)
	metrics.RecordNotification(ctx, "tenant/secret/id", "success", time.Millisecond, 0)
	metrics.RecordFanout(ctx, "completed", 25, 20*time.Millisecond)
	metrics.RecordEligibleFirstAttempt(ctx, "slack", 40*time.Millisecond)
	metrics.RecordDeliveryLane(ctx, "critical", 16, 64)
	metrics.RecordSourceFreshness(ctx, "statuspage", 2*time.Second)
	metrics.RecordSchemaDrift(ctx, "statuspage", "summary")

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"statusmon_source_polls_total",
		"statusmon_reconcile_events_total",
		"statusmon_outbox_age_seconds",
		"statusmon_bus_redeliveries_total",
		"statusmon_notification_sends_total",
		"statusmon_fanout_deliveries_total",
		"statusmon_delivery_eligible_first_attempt_seconds",
		"statusmon_delivery_lane_concurrency",
		"statusmon_delivery_lane_claimed",
		"statusmon_source_freshness_seconds",
		"statusmon_source_schema_drift_total",
		`channel="other"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "tenant/secret/id") {
		t.Fatal("unbounded label value leaked into metrics")
	}
}

func TestBounded(t *testing.T) {
	t.Parallel()
	if got := bounded("statuspage", "unknown"); got != "statuspage" {
		t.Fatalf("bounded(valid) = %q", got)
	}
	if got := bounded("tenant/id", "unknown"); got != "other" {
		t.Fatalf("bounded(dynamic) = %q", got)
	}
	if got := bounded("", "unknown"); got != "unknown" {
		t.Fatalf("bounded(empty) = %q", got)
	}
}
