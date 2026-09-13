package postgres

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const (
	integrationVendorID = "10000000-0000-0000-0000-000000000001"
	integrationSourceID = "20000000-0000-0000-0000-000000000001"
)

type integrationDatabase struct {
	store *Store
	pool  *pgxpool.Pool
}

func openIntegrationDatabase(t *testing.T) integrationDatabase {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create integration admin pool: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping integration database: %v", err)
	}

	schema := fmt.Sprintf("statushub_store_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create integration schema: %v", err)
	}

	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})

	migrationConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	migrationConfig.ConnConfig.RuntimeParams["search_path"] = schema
	migrationConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	migrationPool, err := pgxpool.NewWithConfig(ctx, migrationConfig)
	if err != nil {
		t.Fatalf("create migration pool: %v", err)
	}
	migrations, err := filepath.Glob(filepath.Join(repositoryRoot(t), "migrations", "*.up.sql"))
	if err != nil {
		migrationPool.Close()
		t.Fatalf("list migrations: %v", err)
	}
	sort.Strings(migrations)
	for _, path := range migrations {
		name := filepath.Base(path)
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			migrationPool.Close()
			t.Fatalf("read migration %s: %v", name, readErr)
		}
		if _, execErr := migrationPool.Exec(ctx, string(contents)); execErr != nil {
			migrationPool.Close()
			t.Fatalf("apply migration %s: %v", name, execErr)
		}
	}
	migrationPool.Close()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse application database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create application pool: %v", err)
	}
	t.Cleanup(pool.Close)

	database := integrationDatabase{store: NewWithPool(pool), pool: pool}
	database.insertVendor(t)
	return database
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func (database integrationDatabase) insertVendor(t *testing.T) {
	t.Helper()
	_, err := database.pool.Exec(
		context.Background(),
		`INSERT INTO vendors (id, slug, name) VALUES ($1, $2, $3)`,
		integrationVendorID,
		"integration-vendor",
		"Integration Vendor",
	)
	if err != nil {
		t.Fatalf("insert integration vendor: %v", err)
	}
}

func (database integrationDatabase) insertSource(t *testing.T, id string, nextPollAt time.Time) {
	t.Helper()
	_, err := database.pool.Exec(
		context.Background(),
		`INSERT INTO sources (
             id, vendor_id, requested_url, canonical_url, source_type,
             adapter_name, adapter_version, next_poll_at
         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id,
		integrationVendorID,
		"https://status.example.test/"+id,
		"https://status.example.test/"+id,
		"status_page",
		"statuspage",
		"v1",
		nextPollAt,
	)
	if err != nil {
		t.Fatalf("insert integration source %s: %v", id, err)
	}
}

func TestIntegrationSourceLeaseCAS(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(-time.Minute))

	leases, err := database.store.AcquireDueSources(ctx, "collector-a", 1, time.Minute)
	if err != nil {
		t.Fatalf("acquire first source lease: %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("first lease count = %d, want 1", len(leases))
	}
	first := leases[0]
	if first.ID != integrationSourceID || first.LeaseOwner != "collector-a" || first.LeaseToken == "" {
		t.Fatalf("unexpected first lease: %+v", first)
	}

	if _, err := database.pool.Exec(
		ctx,
		`UPDATE sources SET lease_until = statement_timestamp() - interval '1 second' WHERE id = $1`,
		integrationSourceID,
	); err != nil {
		t.Fatalf("expire first source lease: %v", err)
	}
	leases, err = database.store.AcquireDueSources(ctx, "collector-b", 1, time.Minute)
	if err != nil {
		t.Fatalf("reacquire expired source lease: %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("replacement lease count = %d, want 1", len(leases))
	}
	second := leases[0]
	if second.LeaseToken == first.LeaseToken {
		t.Fatal("reacquired source reused its fencing token")
	}

	err = database.store.CompletePoll(ctx, CompletePollParams{
		SourceID:   integrationSourceID,
		LeaseToken: first.LeaseToken,
		NextPollAt: time.Now().Add(time.Minute),
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale CompletePoll error = %v, want ErrLeaseLost", err)
	}

	checkpoint := "etag:v1"
	err = database.store.CompletePoll(ctx, CompletePollParams{
		SourceID:    integrationSourceID,
		LeaseToken:  second.LeaseToken,
		NextPollAt:  time.Now().Add(-time.Second),
		Checkpoint:  &checkpoint,
		HealthState: "healthy",
	})
	if err != nil {
		t.Fatalf("complete current source lease: %v", err)
	}

	leases, err = database.store.AcquireDueSources(ctx, "collector-c", 1, time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("acquire source for failure: leases=%d err=%v", len(leases), err)
	}
	failureNextPoll := time.Now().Add(time.Hour)
	if err := database.store.FailPoll(ctx, FailPollParams{
		SourceID:    integrationSourceID,
		LeaseToken:  leases[0].LeaseToken,
		NextPollAt:  failureNextPoll,
		HealthState: "degraded",
	}); err != nil {
		t.Fatalf("fail current source lease: %v", err)
	}

	var failureStreak int
	var healthState string
	var leaseToken *string
	var lastCheckpoint *string
	if err := database.pool.QueryRow(
		ctx,
		`SELECT failure_streak, health_state, lease_token, last_checkpoint FROM sources WHERE id = $1`,
		integrationSourceID,
	).Scan(&failureStreak, &healthState, &leaseToken, &lastCheckpoint); err != nil {
		t.Fatalf("read completed source: %v", err)
	}
	if failureStreak != 1 || healthState != "degraded" || leaseToken != nil {
		t.Fatalf("unexpected failed source state: streak=%d health=%s lease=%v", failureStreak, healthState, leaseToken)
	}
	if lastCheckpoint == nil || *lastCheckpoint != checkpoint {
		t.Fatalf("last checkpoint = %v, want %q", lastCheckpoint, checkpoint)
	}
}

func TestIntegrationConcurrentSourceClaimsDoNotOverlap(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	const sourceCount = 10
	for index := 1; index <= sourceCount; index++ {
		database.insertSource(t, integrationUUID(100+index), time.Now().Add(-time.Minute))
	}

	start := make(chan struct{})
	results := make(chan []SourceLease, 2)
	errorsChannel := make(chan error, 2)
	var group sync.WaitGroup
	for _, owner := range []string{"collector-a", "collector-b"} {
		group.Add(1)
		go func(owner string) {
			defer group.Done()
			<-start
			leases, err := database.store.AcquireDueSources(ctx, owner, sourceCount, time.Minute)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- leases
		}(owner)
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatalf("concurrent source claim: %v", err)
	}

	seen := make(map[string]string, sourceCount)
	for leases := range results {
		for _, lease := range leases {
			if previousOwner, exists := seen[lease.ID]; exists {
				t.Fatalf("source %s claimed by both %s and %s", lease.ID, previousOwner, lease.LeaseOwner)
			}
			seen[lease.ID] = lease.LeaseOwner
		}
	}
	if len(seen) != sourceCount {
		t.Fatalf("claimed %d unique sources, want %d", len(seen), sourceCount)
	}
}

func TestIntegrationEventOutboxDedupAndLeaseCAS(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))

	event := integrationEvent("logical-event-1", "github-incident-upstream-501")
	message := OutboxMessage{
		ID:      integrationUUID(601),
		Subject: "status.events.v1",
		Payload: []byte(`{"specversion":"1.0","type":"incident.updated"}`),
	}
	inserted, err := database.store.InsertEventWithOutbox(ctx, event, message)
	if err != nil || !inserted {
		t.Fatalf("insert event with outbox: inserted=%v err=%v", inserted, err)
	}

	duplicate := event
	duplicate.ID = domain.CanonicalEventID(integrationUUID(502))
	duplicateMessage := message
	duplicateMessage.ID = integrationUUID(602)
	inserted, err = database.store.InsertEventWithOutbox(ctx, duplicate, duplicateMessage)
	if err != nil || inserted {
		t.Fatalf("insert duplicate event: inserted=%v err=%v", inserted, err)
	}
	assertTableCount(t, database.pool, "canonical_events", 1)
	assertTableCount(t, database.pool, "outbox", 1)

	rollbackEvent := integrationEvent("logical-event-rollback", integrationUUID(503))
	rollbackMessage := message // duplicate outbox primary key forces rollback.
	inserted, err = database.store.InsertEventWithOutbox(ctx, rollbackEvent, rollbackMessage)
	if err == nil || inserted {
		t.Fatalf("outbox conflict result: inserted=%v err=%v, want transaction failure", inserted, err)
	}
	var rollbackCount int
	if err := database.pool.QueryRow(
		ctx,
		`SELECT count(*) FROM canonical_events WHERE source_event_key = $1`,
		string(rollbackEvent.SourceEventKey),
	).Scan(&rollbackCount); err != nil {
		t.Fatalf("count rolled-back event: %v", err)
	}
	if rollbackCount != 0 {
		t.Fatalf("rolled-back canonical event count = %d, want 0", rollbackCount)
	}

	firstClaim, err := database.store.ClaimOutbox(ctx, "publisher-a", 1, time.Minute)
	if err != nil || len(firstClaim) != 1 {
		t.Fatalf("claim first outbox lease: messages=%d err=%v", len(firstClaim), err)
	}
	if firstClaim[0].Attempts != 1 || firstClaim[0].LeaseToken == "" {
		t.Fatalf("unexpected first outbox lease: %+v", firstClaim[0])
	}
	blockedClaim, err := database.store.ClaimOutbox(ctx, "publisher-b", 1, time.Minute)
	if err != nil || len(blockedClaim) != 0 {
		t.Fatalf("claim active outbox lease: messages=%d err=%v, want none", len(blockedClaim), err)
	}

	if _, err := database.pool.Exec(
		ctx,
		`UPDATE outbox SET lease_until = statement_timestamp() - interval '1 second' WHERE id = $1`,
		firstClaim[0].ID,
	); err != nil {
		t.Fatalf("expire first outbox lease: %v", err)
	}
	secondClaim, err := database.store.ClaimOutbox(ctx, "publisher-b", 1, time.Minute)
	if err != nil || len(secondClaim) != 1 {
		t.Fatalf("reclaim expired outbox lease: messages=%d err=%v", len(secondClaim), err)
	}
	if secondClaim[0].LeaseToken == firstClaim[0].LeaseToken || secondClaim[0].Attempts != 2 {
		t.Fatalf("unexpected replacement outbox lease: %+v", secondClaim[0])
	}

	err = database.store.MarkOutboxPublished(ctx, firstClaim[0].ID, firstClaim[0].LeaseToken, time.Time{})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale MarkOutboxPublished error = %v, want ErrLeaseLost", err)
	}
	if err := database.store.MarkOutboxPublished(
		ctx,
		secondClaim[0].ID,
		secondClaim[0].LeaseToken,
		time.Time{},
	); err != nil {
		t.Fatalf("mark current outbox lease published: %v", err)
	}

	var publishedAt *time.Time
	var leaseOwner *string
	if err := database.pool.QueryRow(
		ctx,
		`SELECT published_at, lease_owner FROM outbox WHERE id = $1`,
		message.ID,
	).Scan(&publishedAt, &leaseOwner); err != nil {
		t.Fatalf("read published outbox row: %v", err)
	}
	if publishedAt == nil || leaseOwner != nil {
		t.Fatalf("published outbox has published_at=%v lease_owner=%v", publishedAt, leaseOwner)
	}
}

func TestIntegrationFailedOutboxIsRescheduled(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))

	event := integrationEvent("logical-event-retry", integrationUUID(701))
	message := OutboxMessage{
		ID:      integrationUUID(702),
		Subject: "status.events.v1",
		Payload: []byte(`{"attempt":"retry"}`),
	}
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, message); err != nil || !inserted {
		t.Fatalf("insert retry event: inserted=%v err=%v", inserted, err)
	}

	claimed, err := database.store.ClaimOutbox(ctx, "publisher-a", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim retry event: messages=%d err=%v", len(claimed), err)
	}
	retryAt := time.Now().Add(time.Hour)
	if err := database.store.FailOutbox(ctx, FailOutboxParams{
		ID:          claimed[0].ID,
		LeaseToken:  claimed[0].LeaseToken,
		AvailableAt: retryAt,
		LastError:   "nats unavailable",
	}); err != nil {
		t.Fatalf("reschedule failed outbox: %v", err)
	}
	claimed, err = database.store.ClaimOutbox(ctx, "publisher-b", 1, time.Minute)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim future retry: messages=%d err=%v, want none", len(claimed), err)
	}

	if _, err := database.pool.Exec(
		ctx,
		`UPDATE outbox SET available_at = statement_timestamp() - interval '1 second' WHERE id = $1`,
		message.ID,
	); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	claimed, err = database.store.ClaimOutbox(ctx, "publisher-b", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim due retry: messages=%d err=%v", len(claimed), err)
	}
	if claimed[0].Attempts != 2 || claimed[0].LastError == nil || *claimed[0].LastError != "nats unavailable" {
		t.Fatalf("unexpected retried outbox state: %+v", claimed[0])
	}
}

func TestIntegrationEnsureDeliveryAbsorbsRedelivery(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))

	event := integrationEvent("logical-event-delivery", integrationUUID(801))
	message := OutboxMessage{
		ID:      integrationUUID(802),
		Subject: "status.events.v1",
		Payload: []byte(`{"delivery":"dedup"}`),
	}
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, message); err != nil || !inserted {
		t.Fatalf("insert delivery event: inserted=%v err=%v", inserted, err)
	}
	var eventID string
	if err := database.pool.QueryRow(
		ctx,
		`SELECT id FROM canonical_events WHERE source_event_key = $1`,
		string(event.SourceEventKey),
	).Scan(&eventID); err != nil {
		t.Fatalf("read delivery event ID: %v", err)
	}

	tenantID := integrationUUID(803)
	subscriptionID := integrationUUID(804)
	endpointID := integrationUUID(805)
	if _, err := database.pool.Exec(
		ctx,
		`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, $3)`,
		tenantID,
		"integration-tenant",
		"Integration Tenant",
	); err != nil {
		t.Fatalf("insert delivery tenant: %v", err)
	}
	if _, err := database.pool.Exec(
		ctx,
		`INSERT INTO subscriptions (id, tenant_id, name, rule) VALUES ($1, $2, $3, '{}'::jsonb)`,
		subscriptionID,
		tenantID,
		"Integration Subscription",
	); err != nil {
		t.Fatalf("insert delivery subscription: %v", err)
	}
	if _, err := database.pool.Exec(
		ctx,
		`INSERT INTO endpoints (id, tenant_id, channel, name, encrypted_config, key_id)
         VALUES ($1, $2, $3, $4, $5, $6)`,
		endpointID,
		tenantID,
		"generic_webhook",
		"Integration Endpoint",
		[]byte("encrypted-test-config"),
		"test-key",
	); err != nil {
		t.Fatalf("insert delivery endpoint: %v", err)
	}

	start := make(chan struct{})
	insertedResults := make(chan bool, 2)
	errorResults := make(chan error, 2)
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			inserted, err := database.store.EnsureDelivery(ctx, Delivery{
				ID:              integrationUUID(806 + index),
				EventID:         eventID,
				SubscriptionID:  subscriptionID,
				EndpointID:      endpointID,
				TemplateVersion: 1,
			})
			if err != nil {
				errorResults <- err
				return
			}
			insertedResults <- inserted
		}(index)
	}
	close(start)
	group.Wait()
	close(insertedResults)
	close(errorResults)
	for err := range errorResults {
		t.Fatalf("ensure concurrent delivery: %v", err)
	}

	insertedCount := 0
	duplicateCount := 0
	for inserted := range insertedResults {
		if inserted {
			insertedCount++
		} else {
			duplicateCount++
		}
	}
	if insertedCount != 1 || duplicateCount != 1 {
		t.Fatalf("delivery results inserted=%d duplicate=%d, want 1/1", insertedCount, duplicateCount)
	}
	assertTableCount(t, database.pool, "deliveries", 1)
}

func integrationEvent(sourceEventKey string, entityID string) domain.CanonicalEvent {
	return domain.CanonicalEvent{
		SourceID:          integrationSourceID,
		Provider:          "statuspage",
		PageID:            "integration",
		Kind:              domain.EventKindIncidentUpdated,
		EntityKind:        domain.EntityIncident,
		EntityID:          entityID,
		AggregateRevision: 2,
		SourceEventKey:    domain.SourceEventKey(sourceEventKey),
		NormalizerVersion: "v1",
		SchemaVersion:     "v1",
		Payload:           []byte(`{"incident":{"phase":"monitoring"}}`),
		ObservedAt:        time.Now().UTC(),
	}
}

func integrationUUID(number int) string {
	return fmt.Sprintf("30000000-0000-0000-0000-%012d", number)
}

func assertTableCount(t *testing.T, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	allowed := []string{"canonical_events", "deliveries", "outbox"}
	if index := sort.SearchStrings(allowed, table); index >= len(allowed) || allowed[index] != table {
		t.Fatalf("unsupported table %q", table)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s count = %d, want %d", table, count, want)
	}
}
