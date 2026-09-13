package e2e_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/bus"
	busjs "github.com/Seeridia/StatusHub/internal/bus/jetstream"
	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/notify"
	"github.com/Seeridia/StatusHub/internal/pipeline/fanout"
	notifierpipeline "github.com/Seeridia/StatusHub/internal/pipeline/notifier"
	outboxpipeline "github.com/Seeridia/StatusHub/internal/pipeline/outbox"
	eventprocessor "github.com/Seeridia/StatusHub/internal/pipeline/processor"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type acceptingDriver struct{}

func (acceptingDriver) Validate(context.Context, notify.Endpoint) error { return nil }
func (acceptingDriver) Render(_ context.Context, event notify.CanonicalEvent, _ notify.Endpoint) (notify.Payload, error) {
	return notify.Payload{Channel: notify.ChannelGenericWebhook, EventID: event.ID, ContentType: "application/json", Body: []byte(`{}`)}, nil
}
func (acceptingDriver) Send(_ context.Context, delivery notify.Delivery, _ notify.Payload) (notify.Receipt, error) {
	return notify.Receipt{Status: notify.StatusProviderAccepted, HTTPStatus: 202, AcceptedAt: time.Now(), ProviderMessageID: "accepted-" + delivery.ID}, nil
}
func (acceptingDriver) Classify(err error) notify.RetryDecision { return notify.ClassifyError(err) }

func TestM2EventToFanoutToNotifier(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	natsURL := os.Getenv("STATUSHUB_TEST_NATS_URL")
	if databaseURL == "" || natsURL == "" {
		t.Skip("set TEST_DATABASE_URL and STATUSHUB_TEST_NATS_URL to run M2 integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repository, pool := openDatabase(t, ctx, databaseURL)
	seedGraph(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE endpoints SET encrypted_config=$2 WHERE id=$1`, endpointID,
		[]byte(`{"url":"https://hooks.example.test/status","secret":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO subscription_endpoints(subscription_id,endpoint_id) VALUES($1,$2)`, subscriptionID, endpointID); err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	subject := "statushub.e2e.m2." + suffix + ".critical"
	processor, err := eventprocessor.New(repository, subject)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.CanonicalEvent{SourceID: sourceID, Kind: domain.EventKindIncidentCreated,
		EntityKind: domain.EntityIncident, EntityID: "incident-m2", AggregateRevision: 1,
		SourceEventKey: "m2-event", NormalizerVersion: "v1", SchemaVersion: "v1",
		Payload: []byte(`{"current":{"name":"API outage","impact":"critical"}}`), ObservedAt: time.Now().UTC()}
	result, err := processor.Persist(ctx, event)
	if err != nil || !result.Inserted {
		t.Fatalf("persist event=%#v err=%v", result, err)
	}

	busConfig := busjs.DefaultConfig()
	busConfig.StreamName = "STATUSHUB_M2_" + suffix
	busConfig.Subjects = []string{"statushub.e2e.m2." + suffix + ".>"}
	busConfig.MaxAge, busConfig.MaxBytes = time.Minute, 1<<20
	busConfig.DuplicateWindow = time.Minute
	eventBus, err := busjs.Connect(ctx, natsURL, busConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer eventBus.Close()
	t.Cleanup(func() { _ = eventBus.DeleteStream(context.Background()) })
	publisherConfig := outboxpipeline.DefaultConfig("m2-publisher")
	publisherConfig.BatchSize, publisherConfig.MaxConcurrency = 1, 1
	publisher, _ := outboxpipeline.New(repository, eventBus, nil, publisherConfig)
	if stats, err := publisher.RunOnce(ctx); err != nil || stats.Published != 1 {
		t.Fatalf("publish stats=%#v err=%v", stats, err)
	}

	fanoutConfig := fanout.DefaultConfig("m2-fanout")
	fanoutConfig.ShardCount, fanoutConfig.ClaimBatchSize, fanoutConfig.MaxConcurrency = 4, 4, 2
	fanoutWorker, _ := fanout.New(repository, nil, fanoutConfig)
	consumer, err := eventBus.Consumer(ctx, busjs.DefaultConsumerConfig("m2_consumer_"+suffix, subject))
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := consumer.ProcessBatch(ctx, 1, time.Second, func(ctx context.Context, delivery busjs.Delivery) error {
		if _, err := bus.DecodeEventEnvelope(delivery.Data); err != nil {
			return err
		}
		return fanoutWorker.HandleMessage(ctx, delivery.Data)
	}); err != nil || processed != 1 {
		t.Fatalf("consume processed=%d err=%v", processed, err)
	}
	if stats, err := fanoutWorker.RunOnce(ctx); err != nil || stats.Inserted != 1 || stats.Completed != 4 {
		t.Fatalf("fanout stats=%#v err=%v", stats, err)
	}

	notifierConfig := notifierpipeline.DefaultConfig("m2-notifier")
	notifierConfig.Lane = store.DeliveryLaneCritical
	notifierConfig.BatchSize, notifierConfig.PerTenant = 4, 1
	notifierConfig.MinConcurrency, notifierConfig.InitialConcurrency, notifierConfig.MaxConcurrency = 1, 1, 1
	notifierWorker, _ := notifierpipeline.New(repository, []notify.ChannelDriver{
		notifierpipeline.Bind(notify.ChannelGenericWebhook, acceptingDriver{}),
	}, notifierpipeline.JSONConfigDecoder{}, nil, notifierConfig)
	if stats, err := notifierWorker.RunOnce(ctx); err != nil || stats.Accepted != 1 {
		t.Fatalf("notifier stats=%#v err=%v", stats, err)
	}
	var deliveryStatus string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count FROM deliveries`).Scan(&deliveryStatus, &attempts); err != nil {
		t.Fatal(err)
	}
	if deliveryStatus != "accepted" || attempts != 1 {
		t.Fatalf("delivery status=%s attempts=%d", deliveryStatus, attempts)
	}
}

func TestIntegrationFanoutLoadBaseline(t *testing.T) {
	deliveryCount, maximum := 25000, 30*time.Second
	contextTimeout := 2 * time.Minute
	if os.Getenv("STATUSHUB_RUN_M4_MILLION") == "1" {
		deliveryCount, maximum, contextTimeout = 1000000, 4*time.Minute, 6*time.Minute
	} else if os.Getenv("STATUSHUB_RUN_M4_LOAD") == "1" {
		deliveryCount, maximum = 250000, 60*time.Second
	} else if os.Getenv("STATUSHUB_RUN_LOAD") != "1" {
		t.Skip("set STATUSHUB_RUN_LOAD=1 for 25k, STATUSHUB_RUN_M4_LOAD=1 for 250k, or STATUSHUB_RUN_M4_MILLION=1 for the million-delivery drill")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the fanout load baseline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	repository, pool := openDatabase(t, ctx, databaseURL)
	seedGraph(t, ctx, pool)
	// Replace the small fixture graph with independently addressable
	// subscriptions. M4 uses 100 tenants to exercise the fair fanout index.
	tenantCount := 10
	if deliveryCount >= 1000000 {
		tenantCount = 400
	} else if deliveryCount >= 250000 {
		tenantCount = 100
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenants`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO tenants(id,slug,name)
SELECT md5('tenant-'||n)::uuid, 'tenant-'||n, 'Tenant '||n FROM generate_series(1,$1) n`, tenantCount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO subscriptions(id,tenant_id,name,rule)
SELECT md5('subscription-'||n)::uuid, md5('tenant-'||((n-1)%$2+1))::uuid,
       'Subscription '||n, '{}'::jsonb
FROM generate_series(1,$1) n`, deliveryCount, tenantCount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id)
SELECT md5('endpoint-'||n)::uuid, md5('tenant-'||((n-1)%$2+1))::uuid,
       'generic_webhook', 'Endpoint '||n,
       convert_to('{"url":"https://hooks.example.test/status","secret":"secret"}','UTF8'), 'key'
FROM generate_series(1,$1) n`, deliveryCount, tenantCount); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO subscription_endpoints(subscription_id,endpoint_id)
SELECT md5('subscription-'||n)::uuid, md5('endpoint-'||n)::uuid
FROM generate_series(1,$1) n`, deliveryCount); err != nil {
		t.Fatal(err)
	}
	event := domain.CanonicalEvent{SourceID: sourceID, Kind: domain.EventKindIncidentCreated,
		EntityKind: domain.EntityIncident, EntityID: "load-incident", AggregateRevision: 1,
		SourceEventKey: "m2-load-event", NormalizerVersion: "v1", SchemaVersion: "v1",
		Payload: []byte(`{"current":{"name":"Load event","impact":"major"}}`), ObservedAt: time.Now().UTC()}
	processor, _ := eventprocessor.New(repository, "statushub.events.normal")
	persisted, err := processor.Persist(ctx, event)
	if err != nil || !persisted.Inserted {
		t.Fatalf("persist load event=%#v err=%v", persisted, err)
	}
	workerConfig := fanout.DefaultConfig("load-fanout")
	workerConfig.PageSize = 1000
	workerConfig.ClaimBatchSize = 16
	workerConfig.MaxConcurrency = 8
	worker, _ := fanout.New(repository, nil, workerConfig)
	envelope, _ := (bus.EventEnvelope{Version: bus.EventEnvelopeVersion, EventID: persisted.EventID,
		SourceID: sourceID, Kind: event.Kind, EntityKind: event.EntityKind, EntityID: event.EntityID,
		AggregateRevision: 1, SchemaVersion: "v1"}).Marshal()
	if err := worker.HandleMessage(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	inserted := int64(0)
	for iteration := 0; iteration < 1000; iteration++ {
		stats, runErr := worker.RunOnce(ctx)
		if runErr != nil {
			t.Fatal(runErr)
		}
		inserted += stats.Inserted
		if stats.Claimed == 0 {
			break
		}
	}
	elapsed := time.Since(started)
	if inserted != int64(deliveryCount) {
		t.Fatalf("inserted=%d want=%d", inserted, deliveryCount)
	}
	if elapsed > maximum {
		t.Fatalf("%d fanout took %s, want <=%s", deliveryCount, elapsed, maximum)
	}
	t.Logf("%d persisted fanout: %s (%.0f deliveries/s)", deliveryCount, elapsed, float64(deliveryCount)/elapsed.Seconds())
}
