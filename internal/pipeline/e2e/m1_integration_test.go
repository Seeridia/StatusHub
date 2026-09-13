package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
	busjs "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus/jetstream"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	outboxpipeline "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/pipeline/outbox"
	eventprocessor "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/pipeline/processor"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/reconcile"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

const (
	vendorID       = "10000000-0000-0000-0000-000000000001"
	sourceID       = "20000000-0000-0000-0000-000000000001"
	tenantID       = "30000000-0000-0000-0000-000000000001"
	subscriptionID = "40000000-0000-0000-0000-000000000001"
	endpointID     = "50000000-0000-0000-0000-000000000001"
)

type crashOnceStore struct {
	inner *store.Store
	mu    sync.Mutex
	crash bool
}

func (s *crashOnceStore) ClaimOutbox(ctx context.Context, owner string, limit int, lease time.Duration) ([]store.OutboxLease, error) {
	return s.inner.ClaimOutbox(ctx, owner, limit, lease)
}

func (s *crashOnceStore) MarkOutboxPublished(ctx context.Context, id, token string, at time.Time) error {
	s.mu.Lock()
	if !s.crash {
		s.crash = true
		s.mu.Unlock()
		return errors.New("simulated crash after broker ack")
	}
	s.mu.Unlock()
	return s.inner.MarkOutboxPublished(ctx, id, token, at)
}

func (s *crashOnceStore) FailOutbox(ctx context.Context, params store.FailOutboxParams) error {
	return s.inner.FailOutbox(ctx, params)
}

func TestM1CrashAndRedeliveryRemainLogicallyIdempotent(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	natsURL := os.Getenv("STATUSMON_TEST_NATS_URL")
	if databaseURL == "" || natsURL == "" {
		t.Skip("set TEST_DATABASE_URL and STATUSMON_TEST_NATS_URL to run M1 integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	repository, pool := openDatabase(t, ctx, databaseURL)
	seedGraph(t, ctx, pool)

	baseline := snapshot(time.Now().UTC(), nil)
	baselineResult, err := reconcile.Apply(reconcile.State{}, baseline)
	if err != nil || len(baselineResult.Events) != 0 || !baselineResult.BaselineEstablished {
		t.Fatalf("baseline result events=%d established=%v err=%v", len(baselineResult.Events), baselineResult.BaselineEstablished, err)
	}
	incident := domain.Incident{
		ID: "github-upstream-incident", Kind: domain.IncidentKindIncident,
		Name: "API degraded", Phase: domain.IncidentPhaseInvestigating,
		RawPhase: "investigating", Impact: domain.ImpactMajor, RawImpact: "major",
	}
	changedResult, err := reconcile.Apply(baselineResult.State, snapshot(time.Now().UTC().Add(time.Second), []domain.Incident{incident}))
	if err != nil || len(changedResult.Events) != 1 {
		t.Fatalf("changed reconcile events=%d err=%v", len(changedResult.Events), err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	subject := "statusmon.e2e." + suffix + ".normal"
	processor, err := eventprocessor.New(repository, subject)
	if err != nil {
		t.Fatalf("create event processor: %v", err)
	}
	firstPersist, err := processor.Persist(ctx, changedResult.Events[0])
	if err != nil || !firstPersist.Inserted {
		t.Fatalf("persist first event: %+v err=%v", firstPersist, err)
	}
	secondPersist, err := processor.Persist(ctx, changedResult.Events[0])
	if err != nil || secondPersist.Inserted {
		t.Fatalf("persist duplicate event: %+v err=%v", secondPersist, err)
	}
	assertCount(t, ctx, pool, "canonical_events", 1)
	assertCount(t, ctx, pool, "outbox", 1)

	busConfig := busjs.DefaultConfig()
	busConfig.StreamName = "STATUSMON_E2E_" + suffix
	busConfig.Subjects = []string{"statusmon.e2e." + suffix + ".>"}
	busConfig.MaxAge = time.Minute
	busConfig.MaxBytes = 1 << 20
	busConfig.DuplicateWindow = time.Minute
	eventBus, err := busjs.Connect(ctx, natsURL, busConfig)
	if err != nil {
		t.Fatalf("connect event bus: %v", err)
	}
	defer eventBus.Close()
	t.Cleanup(func() { _ = eventBus.DeleteStream(context.Background()) })

	publisherConfig := outboxpipeline.DefaultConfig("e2e-publisher")
	publisherConfig.BatchSize = 1
	publisherConfig.MaxConcurrency = 1
	publisherConfig.LeaseDuration = 150 * time.Millisecond
	publisher, err := outboxpipeline.New(&crashOnceStore{inner: repository}, eventBus, nil, publisherConfig)
	if err != nil {
		t.Fatalf("create outbox publisher: %v", err)
	}
	firstRun, err := publisher.RunOnce(ctx)
	if err == nil || firstRun.Failed != 1 {
		t.Fatalf("first publisher run = %+v err=%v, want simulated mark crash", firstRun, err)
	}
	time.Sleep(200 * time.Millisecond)
	secondRun, err := publisher.RunOnce(ctx)
	if err != nil || secondRun.Published != 1 || secondRun.Duplicates != 1 {
		t.Fatalf("second publisher run = %+v err=%v, want duplicate broker ack", secondRun, err)
	}

	consumerConfig := busjs.DefaultConsumerConfig("e2e_consumer_"+suffix, subject)
	consumerConfig.AckWait = 250 * time.Millisecond
	consumerConfig.NakDelay = 25 * time.Millisecond
	consumer, err := eventBus.Consumer(ctx, consumerConfig)
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	attempt := 0
	handler := func(ctx context.Context, delivery busjs.Delivery) error {
		envelope, err := bus.DecodeEventEnvelope(delivery.Data)
		if err != nil {
			return err
		}
		attempt++
		inserted, err := repository.EnsureDelivery(ctx, store.Delivery{
			ID: uuid.NewString(), EventID: string(envelope.EventID),
			SubscriptionID: subscriptionID, EndpointID: endpointID,
			TemplateVersion: 1,
		})
		if err != nil {
			return err
		}
		if attempt == 1 {
			if !inserted {
				return errors.New("first delivery write was unexpectedly deduplicated")
			}
			return errors.New("simulated crash after delivery commit and before ack")
		}
		if inserted {
			return errors.New("redelivery created a duplicate logical delivery")
		}
		if delivery.NumDelivered < 2 {
			return fmt.Errorf("delivery count = %d, want redelivery", delivery.NumDelivered)
		}
		return nil
	}
	if processed, err := consumer.ProcessBatch(ctx, 1, time.Second, handler); err == nil || processed != 0 {
		t.Fatalf("first consume = (%d, %v), want simulated crash", processed, err)
	}
	time.Sleep(50 * time.Millisecond)
	if processed, err := consumer.ProcessBatch(ctx, 1, 2*time.Second, handler); err != nil || processed != 1 {
		t.Fatalf("redelivery consume = (%d, %v), want success", processed, err)
	}
	if attempt != 2 {
		t.Fatalf("consumer attempts = %d, want 2", attempt)
	}
	assertCount(t, ctx, pool, "deliveries", 1)
}

func snapshot(observedAt time.Time, incidents []domain.Incident) domain.Snapshot {
	return domain.Snapshot{
		Source:       domain.Source{ID: sourceID, Provider: "atlassian-statuspage", PageID: "github"},
		ResourceKind: domain.ResourceUnresolvedIncidents,
		Incidents:    incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceUnresolvedIncidents},
		ObservedAt:       observedAt, SchemaVersion: "v2", AdapterVersion: "statuspage-v2/1",
		NormalizerVersion: "statuspage-v2/1",
	}
}

func openDatabase(t *testing.T, ctx context.Context, databaseURL string) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("statusmon_e2e_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE") })

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open schema pool: %v", err)
	}
	t.Cleanup(pool.Close)

	migrations, err := filepath.Glob(filepath.Join(repositoryRoot(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	sort.Strings(migrations)
	for _, path := range migrations {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", filepath.Base(path), err)
		}
	}
	return store.NewWithPool(pool), pool
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func seedGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []string{
		`INSERT INTO vendors (id, slug, name) VALUES ('` + vendorID + `', 'github', 'GitHub')`,
		`INSERT INTO sources (id, vendor_id, requested_url, canonical_url, source_type, next_poll_at)
		 VALUES ('` + sourceID + `', '` + vendorID + `', 'https://www.githubstatus.com', 'https://www.githubstatus.com', 'status_page', now())`,
		`INSERT INTO tenants (id, slug, name) VALUES ('` + tenantID + `', 'e2e', 'E2E')`,
		`INSERT INTO subscriptions (id, tenant_id, name, rule)
		 VALUES ('` + subscriptionID + `', '` + tenantID + `', 'All incidents', '{}'::jsonb)`,
		`INSERT INTO endpoints (id, tenant_id, channel, name, encrypted_config, key_id)
		 VALUES ('` + endpointID + `', '` + tenantID + `', 'generic_webhook', 'E2E', '\x00', 'test')`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("seed graph: %v", err)
		}
	}
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	allowed := map[string]bool{"canonical_events": true, "outbox": true, "deliveries": true}
	if !allowed[table] {
		t.Fatalf("unsupported table %q", table)
	}
	var got int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
