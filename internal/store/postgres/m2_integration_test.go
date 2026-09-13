package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIntegrationFanoutDeliveryLeaseAndCallback(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))

	event := integrationEvent("m2-fanout-event", "incident-upstream")
	event.Payload = []byte(`{"current":{"name":"API unavailable","impact":"critical","component_ids":["api"]}}`)
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, OutboxMessage{ID: integrationUUID(920), Subject: "statushub.events.critical", Payload: []byte(`{}`)}); err != nil || !inserted {
		t.Fatalf("insert event: inserted=%v err=%v", inserted, err)
	}
	var eventID string
	if err := database.pool.QueryRow(ctx, `SELECT id FROM canonical_events WHERE source_event_key=$1`, string(event.SourceEventKey)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	tenantID, subscriptionID, endpointID := integrationUUID(921), integrationUUID(922), integrationUUID(923)
	fixtures := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenants(id,slug,name) VALUES($1,'m2-tenant','M2 Tenant')`, []any{tenantID}},
		{`INSERT INTO subscriptions(id,tenant_id,name,rule) VALUES($1,$2,'Critical API','{"minimum_impact":"major"}')`, []any{subscriptionID, tenantID}},
		{`INSERT INTO subscription_scopes(id,subscription_id,vendor_id,component_key,event_kind) VALUES($1,$2,$3,'api','incident.updated')`, []any{integrationUUID(924), subscriptionID, integrationVendorID}},
		{`INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id) VALUES($1,$2,'generic_webhook','Webhook',$3,'key-1')`, []any{endpointID, tenantID, []byte(`{"url":"https://hooks.example.test/status","secret":"secret"}`)}},
		{`INSERT INTO subscription_endpoints(subscription_id,endpoint_id) VALUES($1,$2)`, []any{subscriptionID, endpointID}},
	}
	for _, fixture := range fixtures {
		if _, err := database.pool.Exec(ctx, fixture.query, fixture.args...); err != nil {
			t.Fatalf("insert fanout fixture: %v", err)
		}
	}
	planID, created, err := database.store.EnsureFanoutPlan(ctx, eventID, 4)
	if err != nil || !created || planID == "" {
		t.Fatalf("ensure plan: id=%s created=%v err=%v", planID, created, err)
	}
	if _, duplicate, err := database.store.EnsureFanoutPlan(ctx, eventID, 4); err != nil || duplicate {
		t.Fatalf("duplicate plan: created=%v err=%v", duplicate, err)
	}
	leases, err := database.store.ClaimFanoutShards(ctx, "fanout-a", 4, time.Minute)
	if err != nil || len(leases) != 4 {
		t.Fatalf("claim shards: count=%d err=%v", len(leases), err)
	}
	insertedDeliveries := int64(0)
	for _, lease := range leases {
		candidates, cursor, completed, loadErr := database.store.LoadFanoutCandidates(ctx, lease, 100)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		deliveries := make([]FanoutDelivery, 0, len(candidates))
		for _, candidate := range candidates {
			deliveries = append(deliveries, FanoutDelivery{ID: uuid.NewString(), SubscriptionID: candidate.SubscriptionID,
				EndpointID: candidate.EndpointID, RuleVersion: candidate.RuleVersion, TemplateVersion: 1,
				Priority: 100, EligibleAt: time.Now().Add(-time.Second)})
		}
		inserted, commitErr := database.store.CommitFanoutShard(ctx, CommitFanoutShardParams{PlanID: lease.PlanID,
			ShardNumber: lease.ShardNumber, LeaseToken: lease.LeaseToken, CursorSubscriptionID: cursor,
			Completed: completed, Deliveries: deliveries})
		if commitErr != nil {
			t.Fatal(commitErr)
		}
		insertedDeliveries += inserted
	}
	if insertedDeliveries != 1 {
		t.Fatalf("inserted deliveries=%d", insertedDeliveries)
	}

	deliveryLeases, err := database.store.ClaimDeliveryLane(ctx, "notifier-a", DeliveryLaneCritical, 10, 1, time.Minute)
	if err != nil || len(deliveryLeases) != 1 {
		t.Fatalf("claim delivery: count=%d err=%v", len(deliveryLeases), err)
	}
	lease := deliveryLeases[0]
	if lease.AttemptNumber != 1 || lease.EventKind != event.Kind || len(lease.EncryptedConfig) == 0 {
		t.Fatalf("delivery lease=%#v", lease)
	}
	if _, err := database.pool.Exec(ctx, `UPDATE deliveries SET lease_until=statement_timestamp()-interval '1 second' WHERE id=$1`, lease.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := database.store.ClaimDeliveryLane(ctx, "notifier-b", DeliveryLaneRetry, 10, 1, time.Minute)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].AttemptNumber != 2 || reclaimed[0].LeaseToken == lease.LeaseToken {
		t.Fatalf("reclaimed=%#v err=%v", reclaimed, err)
	}
	if err := database.store.CompleteDeliveryAttempt(ctx, CompleteDeliveryParams{DeliveryID: lease.ID,
		LeaseToken: lease.LeaseToken, AttemptNumber: lease.AttemptNumber, Status: "accepted", FinishedAt: time.Now()}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale completion error=%v, want ErrLeaseLost", err)
	}
	lease = reclaimed[0]
	finished := time.Now().UTC()
	if err := database.store.CompleteDeliveryAttempt(ctx, CompleteDeliveryParams{DeliveryID: lease.ID,
		LeaseToken: lease.LeaseToken, AttemptNumber: lease.AttemptNumber, Status: "accepted",
		ProviderMessageID: "provider-1", HTTPStatus: 202, FinishedAt: finished}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"notificationType":"Delivery"}`)
	hash := sha256.Sum256(body)
	applied, matched, err := database.store.ApplyProviderCallback(ctx, ProviderCallback{EndpointID: endpointID,
		ProviderEventID: "callback-1", ProviderMessageID: "provider-1", ReceivedAt: finished.Add(time.Second),
		RawBodySHA256: hash[:], Payload: body, DeliveryStatus: "delivered"})
	if err != nil || !applied || !matched {
		t.Fatalf("callback applied=%v matched=%v err=%v", applied, matched, err)
	}
	applied, matched, err = database.store.ApplyProviderCallback(ctx, ProviderCallback{EndpointID: endpointID,
		ProviderEventID: "callback-1", ProviderMessageID: "provider-1", ReceivedAt: finished.Add(time.Second),
		RawBodySHA256: hash[:], Payload: body, DeliveryStatus: "delivered"})
	if err != nil || applied || matched {
		t.Fatalf("duplicate callback applied=%v matched=%v err=%v", applied, matched, err)
	}
	var status string
	if err := database.pool.QueryRow(ctx, `SELECT status FROM deliveries WHERE id=$1`, lease.ID).Scan(&status); err != nil || status != "delivered" {
		t.Fatalf("delivery status=%q err=%v", status, err)
	}
}

func TestIntegrationDeliveryClaimIsTenantFair(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))
	event := integrationEvent("tenant-fair-event", "fair-incident")
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, OutboxMessage{ID: integrationUUID(950), Subject: "statushub.events.normal", Payload: []byte(`{}`)}); err != nil || !inserted {
		t.Fatalf("insert event: inserted=%v err=%v", inserted, err)
	}
	var eventID string
	if err := database.pool.QueryRow(ctx, `SELECT id FROM canonical_events WHERE source_event_key=$1`, string(event.SourceEventKey)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB := integrationUUID(951), integrationUUID(952)
	subscriptionA, subscriptionB := integrationUUID(953), integrationUUID(954)
	endpointA1, endpointA2, endpointB := integrationUUID(955), integrationUUID(956), integrationUUID(957)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenants(id,slug,name) VALUES($1,'fair-a','Fair A'),($2,'fair-b','Fair B')`, []any{tenantA, tenantB}},
		{`INSERT INTO subscriptions(id,tenant_id,name,rule) VALUES($1,$2,'A','{}'),($3,$4,'B','{}')`, []any{subscriptionA, tenantA, subscriptionB, tenantB}},
		{`INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id) VALUES
            ($1,$2,'slack','A1',$6,'key'),($3,$2,'slack','A2',$6,'key'),($4,$5,'slack','B',$6,'key')`, []any{endpointA1, tenantA, endpointA2, endpointB, tenantB, []byte(`{"url":"https://hooks.slack.com/test"}`)}},
		{`INSERT INTO deliveries(id,event_id,subscription_id,endpoint_id,template_version,next_attempt_at)
            VALUES($1,$2,$3,$4,1,now()),($5,$2,$3,$6,1,now()),($7,$2,$8,$9,1,now())`,
			[]any{integrationUUID(958), eventID, subscriptionA, endpointA1, integrationUUID(959), endpointA2, integrationUUID(960), subscriptionB, endpointB}},
	}
	for _, statement := range statements {
		if _, err := database.pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	leases, err := database.store.ClaimDeliveryLane(ctx, "fair-worker", DeliveryLaneBulk, 2, 1, time.Minute)
	if err != nil || len(leases) != 2 {
		t.Fatalf("leases=%#v err=%v", leases, err)
	}
	foundA, foundB := false, false
	for _, lease := range leases {
		foundA = foundA || lease.EndpointID == endpointA1 || lease.EndpointID == endpointA2
		foundB = foundB || lease.EndpointID == endpointB
	}
	if !foundA || !foundB {
		t.Fatalf("tenant fair claim endpoint IDs=%s,%s", leases[0].EndpointID, leases[1].EndpointID)
	}
}
