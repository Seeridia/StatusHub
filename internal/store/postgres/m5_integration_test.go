package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"
)

const (
	m5TenantA       = "50000000-0000-0000-0000-000000000001"
	m5TenantB       = "50000000-0000-0000-0000-000000000002"
	m5EndpointA     = "51000000-0000-0000-0000-000000000001"
	m5EndpointB     = "51000000-0000-0000-0000-000000000002"
	m5SubscriptionA = "52000000-0000-0000-0000-000000000001"
	m5SubscriptionB = "52000000-0000-0000-0000-000000000002"
	m5PrivateSource = "53000000-0000-0000-0000-000000000001"
	m5PrivateEvent  = "54000000-0000-0000-0000-000000000001"
	m5Rollout       = "56000000-0000-0000-0000-000000000001"
)

func TestIntegrationM5ControlPlaneAndTenantPrivateFanout(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	_, err := database.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES
  ($1,'m5-a','M5 Tenant A'),($2,'m5-b','M5 Tenant B')`, m5TenantA, m5TenantB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO regions(id) VALUES('local')`); err != nil {
		t.Fatal(err)
	}
	actor := AuditActor{Type: "user", ID: "integration-test", RequestID: "request-1"}
	for _, endpoint := range []struct{ id, tenant, name string }{{m5EndpointA, m5TenantA, "A"}, {m5EndpointB, m5TenantB, "B"}} {
		created, err := database.store.CreateEndpoint(ctx, CreateEndpointParams{ID: endpoint.id, TenantID: endpoint.tenant,
			Channel: "slack", Name: endpoint.name, Enabled: true, EncryptedConfig: []byte("ciphertext"),
			KeyID: "test-v1", SecretVersion: 1, RateLimits: json.RawMessage(`{}`), Actor: actor})
		if err != nil || created.TenantID != endpoint.tenant {
			t.Fatalf("create endpoint=%#v err=%v", created, err)
		}
	}
	for _, item := range []struct{ id, tenant, endpoint string }{{m5SubscriptionA, m5TenantA, m5EndpointA}, {m5SubscriptionB, m5TenantB, m5EndpointB}} {
		created, err := database.store.CreateSubscription(ctx, CreateSubscriptionParams{ID: item.id, TenantID: item.tenant,
			Name: "all incidents", Enabled: true, Rule: json.RawMessage(`{}`), EndpointIDs: []string{item.endpoint}, Actor: actor})
		if err != nil || created.RuleVersion != 1 || len(created.EndpointIDs) != 1 {
			t.Fatalf("create subscription=%#v err=%v", created, err)
		}
	}
	_, err = database.pool.Exec(ctx, `INSERT INTO sources(id,tenant_id,vendor_id,requested_url,canonical_url,source_type,adapter_name,adapter_version,next_poll_at)
VALUES($1,$2,$3,'https://private.example.test','https://private.example.test','status_page','test','1',statement_timestamp())`,
		m5PrivateSource, m5TenantA, integrationVendorID)
	if err != nil {
		t.Fatal(err)
	}
	rollout, err := database.store.CreateAdapterRollout(ctx, CreateAdapterRolloutParams{ID: m5Rollout,
		SourceID: m5PrivateSource, ScopeTenantID: m5TenantA, CandidateAdapterName: "test-v2",
		CandidateAdapterVersion: "2", SampleRate: 1, MinimumSamples: 1,
		MaximumMismatchRate: 0, MaximumErrorRate: 0, CreatedBy: "integration-test"})
	if err != nil || rollout.ID != m5Rollout {
		t.Fatalf("create tenant rollout=%#v err=%v", rollout, err)
	}
	replayedRollout, err := database.store.CreateAdapterRollout(ctx, CreateAdapterRolloutParams{ID: m5Rollout,
		SourceID: m5PrivateSource, ScopeTenantID: m5TenantA, CandidateAdapterName: "test-v2",
		CandidateAdapterVersion: "2", SampleRate: 1, MinimumSamples: 1,
		MaximumMismatchRate: 0, MaximumErrorRate: 0, CreatedBy: "integration-test"})
	if err != nil || replayedRollout.ID != m5Rollout {
		t.Fatalf("replay tenant rollout=%#v err=%v", replayedRollout, err)
	}
	view, err := database.store.TenantLatestAdapterRollout(ctx, m5TenantA, m5PrivateSource)
	if err != nil || view.Rollout.ID != m5Rollout || view.Rollout.State != "shadow" {
		t.Fatalf("latest tenant rollout=%#v err=%v", view, err)
	}
	if _, err := database.store.TenantAdapterRollout(ctx, m5TenantB, m5Rollout); err != ErrNotFound {
		t.Fatalf("cross-tenant rollout read error=%v", err)
	}
	if _, err := database.store.TenantLatestAdapterRollout(ctx, m5TenantB, m5PrivateSource); err != ErrNotFound {
		t.Fatalf("cross-tenant latest rollout read error=%v", err)
	}
	if _, err := database.store.RollbackAdapterRollout(ctx, DecideAdapterRolloutParams{RolloutID: m5Rollout,
		ScopeTenantID: m5TenantB, ChangedBy: "integration-test", Reason: "cross-tenant attempt"}); err == nil {
		t.Fatal("cross-tenant rollout decision was accepted")
	}
	_, err = database.pool.Exec(ctx, `INSERT INTO canonical_events(id,source_id,entity_type,entity_id,event_kind,aggregate_revision,source_event_key,
  normalizer_version,canonical_schema_version,canonical_payload,observed_at)
VALUES($1,$2,'incident','private-incident','incident.created',1,'private-event','1','v1',
  '{"current":{"name":"Private incident","impact":"major"}}',statement_timestamp())`, m5PrivateEvent, m5PrivateSource)
	if err != nil {
		t.Fatal(err)
	}
	candidates, _, _, err := database.store.LoadFanoutCandidates(ctx, FanoutShardLease{EventID: m5PrivateEvent, ShardCount: 1, ShardNumber: 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].SubscriptionID != m5SubscriptionA {
		t.Fatalf("private source fanout candidates=%#v", candidates)
	}

	endpoints, err := database.store.ListEndpoints(ctx, m5TenantA, nil, 10)
	if err != nil || len(endpoints) != 1 || endpoints[0].ID != m5EndpointA {
		t.Fatalf("tenant endpoint list=%#v err=%v", endpoints, err)
	}
	subscriptions, err := database.store.ListSubscriptions(ctx, m5TenantA, nil, 10)
	if err != nil || len(subscriptions) != 1 || subscriptions[0].ID != m5SubscriptionA {
		t.Fatalf("tenant subscription list=%#v err=%v", subscriptions, err)
	}

	digest := sha256.Sum256([]byte(`{"name":"A"}`))
	record, leader, err := database.store.BeginIdempotency(ctx, IdempotencyRecord{TenantID: m5TenantA,
		Key: "create-a", Method: "POST", Route: "endpoints.create", RequestHash: digest[:], ResourceID: m5EndpointA})
	if err != nil || !leader || record.ResourceID != m5EndpointA {
		t.Fatalf("idempotency leader=%v record=%#v err=%v", leader, record, err)
	}
	if err := database.store.CompleteIdempotency(ctx, m5TenantA, "create-a", 201, json.RawMessage(`{"id":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	replayed, leader, err := database.store.BeginIdempotency(ctx, IdempotencyRecord{TenantID: m5TenantA,
		Key: "create-a", Method: "POST", Route: "endpoints.create", RequestHash: digest[:]})
	if err != nil || leader || replayed.ResponseStatus != 201 {
		t.Fatalf("idempotency replay leader=%v record=%#v err=%v", leader, replayed, err)
	}
	if _, _, err := database.store.BeginIdempotency(ctx, IdempotencyRecord{TenantID: m5TenantA,
		Key: "create-a", Method: "POST", Route: "/v1/tenants/m5-a/subscriptions", RequestHash: digest[:]}); err != ErrIdempotencyConflict {
		t.Fatalf("cross-route idempotency reuse error=%v", err)
	}
	if _, err := database.pool.Exec(ctx, `UPDATE api_idempotency_keys SET expires_at=statement_timestamp()-interval '1 second' WHERE tenant_id=$1 AND idempotency_key='create-a'`, m5TenantA); err != nil {
		t.Fatal(err)
	}
	recycledDigest := sha256.Sum256([]byte(`{"name":"B"}`))
	recycled, leader, err := database.store.BeginIdempotency(ctx, IdempotencyRecord{TenantID: m5TenantA,
		Key: "create-a", Method: "POST", Route: "/v1/tenants/m5-a/subscriptions", RequestHash: recycledDigest[:]})
	if err != nil || !leader || recycled.State != "processing" {
		t.Fatalf("expired idempotency recycle leader=%v record=%#v err=%v", leader, recycled, err)
	}

	jobID := "55000000-0000-0000-0000-000000000001"
	job, err := database.store.CreateEndpointTest(ctx, m5TenantA, m5EndpointA, jobID, actor)
	if err != nil || job.Status != "queued" {
		t.Fatalf("endpoint test job=%#v err=%v", job, err)
	}
	replayedJob, err := database.store.CreateEndpointTest(ctx, m5TenantA, m5EndpointA, jobID, actor)
	if err != nil || replayedJob.ID != jobID || replayedJob.Status != "queued" {
		t.Fatalf("replayed endpoint test job=%#v err=%v", replayedJob, err)
	}
	leases, err := database.store.ClaimEndpointTests(ctx, "test-worker", 1, time.Minute)
	if err != nil || len(leases) != 1 || leases[0].ID != jobID {
		t.Fatalf("endpoint test leases=%#v err=%v", leases, err)
	}
	if err := database.store.CompleteEndpointTest(ctx, CompleteEndpointTestParams{ID: jobID,
		LeaseToken: leases[0].LeaseToken, Succeeded: true, HTTPStatus: 200}); err != nil {
		t.Fatal(err)
	}
	completed, err := database.store.EndpointTest(ctx, m5TenantA, jobID)
	if err != nil || completed.Status != "succeeded" {
		t.Fatalf("completed endpoint test=%#v err=%v", completed, err)
	}

	events, err := database.store.ListTenantEvents(ctx, m5TenantB, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ID == m5PrivateEvent {
			t.Fatal("tenant B could read tenant A private event")
		}
	}
	if _, err := database.store.TenantEvent(ctx, m5TenantB, m5PrivateEvent); err != ErrNotFound {
		t.Fatalf("cross-tenant private event error=%v", err)
	}
}
