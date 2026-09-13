package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegrationResourceSoftDelete(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	tenant, endpoint, rule := integrationUUID(9801), integrationUUID(9802), integrationUUID(9803)
	actor := AuditActor{Type: "user", ID: "deletion-test"}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants(id,slug,name) VALUES($1,'delete-test','Delete test')`, []any{tenant}},
		{`INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id) VALUES($1,$2,'generic_webhook','Test','{}','test')`, []any{endpoint, tenant}},
		{`INSERT INTO subscriptions(id,tenant_id,name,rule) VALUES($1,$2,'Test','{}')`, []any{rule, tenant}},
		{`INSERT INTO subscription_endpoints(subscription_id,endpoint_id) VALUES($1,$2)`, []any{rule, endpoint}},
	} {
		if _, err := db.pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.store.DeleteResource(ctx, integrationUUID(9899), endpoint, "endpoint", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
	db.insertSource(t, integrationSourceID, time.Now())
	event := integrationEvent("soft-delete-event", "soft-delete-incident")
	if _, err := db.store.InsertEventWithOutbox(ctx, event, OutboxMessage{ID: integrationUUID(9804), Subject: "statushub.events.critical", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := db.pool.QueryRow(ctx, `SELECT id FROM canonical_events WHERE source_event_key=$1`, string(event.SourceEventKey)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO deliveries(id,event_id,subscription_id,endpoint_id,template_version,status,priority,next_attempt_at) VALUES($1,$2,$3,$4,1,'queued',100,now())`, integrationUUID(9805), eventID, rule, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := db.store.DeleteResource(ctx, tenant, endpoint, "endpoint", actor); err != nil {
		t.Fatal(err)
	}
	var retained, enabled bool
	if err := db.pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL,enabled FROM endpoints WHERE id=$1`, endpoint).Scan(&retained, &enabled); err != nil || !retained || enabled {
		t.Fatalf("tombstone: %v %v %v", retained, enabled, err)
	}
	list, err := db.store.ListEndpoints(ctx, tenant, nil, 100)
	if err != nil || len(list) != 0 {
		t.Fatalf("list: %v %v", list, err)
	}
	sub, err := db.store.Subscription(ctx, tenant, rule)
	if err != nil || sub.Enabled || len(sub.EndpointIDs) != 0 || sub.RuleVersion != 2 {
		t.Fatalf("rule cleanup: %+v %v", sub, err)
	}
	if _, err := db.store.SetEndpointEnabled(ctx, tenant, endpoint, true, actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-enable: %v", err)
	}
	if err := db.store.DeleteResource(ctx, tenant, rule, "subscription", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.store.Subscription(ctx, tenant, rule); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted rule visible: %v", err)
	}
	if err := db.store.DeleteResource(ctx, tenant, integrationSourceID, "source", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("shared source: %v", err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE sources SET tenant_id=$1 WHERE id=$2`, tenant, integrationSourceID); err != nil {
		t.Fatal(err)
	}
	if err := db.store.DeleteResource(ctx, tenant, integrationSourceID, "source", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.store.SetSourceEnabled(ctx, tenant, integrationSourceID, true, actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source re-enable: %v", err)
	}
	events, err := db.store.ListTenantEvents(ctx, tenant, nil, 100)
	if err != nil || len(events) != 1 {
		t.Fatalf("event history lost: %v %v", events, err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM deliveries WHERE id=$1`, integrationUUID(9805)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delivery history lost: %v", err)
	}
	leases, err := db.store.ClaimDeliveryLane(ctx, "test", DeliveryLaneCritical, 10, 10, time.Minute)
	if err != nil || len(leases) != 0 {
		t.Fatalf("deleted configuration claimed: %v %v", leases, err)
	}
	_, err = db.store.CreateSubscription(ctx, CreateSubscriptionParams{ID: integrationUUID(9806), TenantID: tenant, Name: "Invalid", Enabled: true, Rule: json.RawMessage(`{}`), EndpointIDs: []string{endpoint}, Actor: actor})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("deleted endpoint reattached: %v", err)
	}
	for _, suffix := range []string{"down", "up"} {
		contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "migrations", "000014_resource_soft_delete."+suffix+".sql"))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, string(contents)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

}
