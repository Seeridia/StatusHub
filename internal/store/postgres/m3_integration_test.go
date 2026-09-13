package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIntegrationDeadLetterListAndGuardedReplay(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))
	event := integrationEvent("m3-dlq-event", "m3-dlq-incident")
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, OutboxMessage{ID: integrationUUID(1100), Subject: "statusmon.events.normal", Payload: []byte(`{}`)}); err != nil || !inserted {
		t.Fatalf("insert event: inserted=%v err=%v", inserted, err)
	}
	var eventID string
	if err := database.pool.QueryRow(ctx, `SELECT id FROM canonical_events WHERE source_event_key=$1`, string(event.SourceEventKey)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	tenantID, subscriptionID := integrationUUID(1101), integrationUUID(1102)
	if _, err := database.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'m3-dlq','M3 DLQ')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO subscriptions(id,tenant_id,name,rule) VALUES($1,$2,'M3 DLQ','{}')`, subscriptionID, tenantID); err != nil {
		t.Fatal(err)
	}

	type fixture struct {
		deliveryID string
		endpointID string
		enabled    bool
		expiresAt  any
		attemptKey int
	}
	now := time.Now().UTC()
	fixtures := []fixture{
		{integrationUUID(1110), integrationUUID(1120), true, nil, 1},
		{integrationUUID(1111), integrationUUID(1121), false, nil, 1},
		{integrationUUID(1112), integrationUUID(1122), true, now.Add(-time.Minute), 1},
		{integrationUUID(1113), integrationUUID(1123), true, nil, 2},
	}
	for index, item := range fixtures {
		if _, err := database.pool.Exec(ctx, `INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id,secret_version,enabled)
VALUES($1,$2,'slack',$3,$4,'key',1,$5)`, item.endpointID, tenantID, "endpoint", []byte(`{"url":"https://hooks.example.test"}`), item.enabled); err != nil {
			t.Fatalf("insert endpoint fixture %d: %v", index, err)
		}
		if _, err := database.pool.Exec(ctx, `INSERT INTO subscription_endpoints(subscription_id,endpoint_id) VALUES($1,$2)`, subscriptionID, item.endpointID); err != nil {
			t.Fatalf("insert subscription endpoint fixture %d: %v", index, err)
		}
		deadLetteredAt := now.Add(-time.Duration(index) * time.Minute)
		if _, err := database.pool.Exec(ctx, `INSERT INTO deliveries(id,event_id,subscription_id,endpoint_id,template_version,status,attempt_count,
                       dead_lettered_at,dead_letter_reason,expires_at)
VALUES($1,$2,$3,$4,$5,'dead_letter',1,$6,'provider timeout',$7)`, item.deliveryID, eventID, subscriptionID,
			item.endpointID, index+1, deadLetteredAt, item.expiresAt); err != nil {
			t.Fatalf("insert delivery fixture %d: %v", index, err)
		}
		if _, err := database.pool.Exec(ctx, `INSERT INTO delivery_attempts(id,delivery_id,attempt_number,secret_version,status,started_at,finished_at)
VALUES($1,$2,1,$3,'dead_letter',$4,$4)`, integrationUUID(1130+index), item.deliveryID, item.attemptKey, deadLetteredAt); err != nil {
			t.Fatalf("insert fixture %d: %v", index, err)
		}
	}

	listed, err := database.store.ListDeadLetters(ctx, nil, 10)
	if err != nil || len(listed) != 4 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	if listed[0].Reason != "provider timeout" || listed[0].AttemptCount != 1 {
		t.Fatalf("first dead letter=%#v", listed[0])
	}
	if replayed, err := database.store.ReplayDeadLetter(ctx, fixtures[0].deliveryID, now); err != nil || !replayed {
		t.Fatalf("valid replay replayed=%v err=%v", replayed, err)
	}
	var status string
	var replayCount int
	if err := database.pool.QueryRow(ctx, `SELECT status,replay_count FROM deliveries WHERE id=$1`, fixtures[0].deliveryID).Scan(&status, &replayCount); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || replayCount != 1 {
		t.Fatalf("status=%q replay_count=%d", status, replayCount)
	}
	for _, item := range fixtures[1:] {
		if replayed, err := database.store.ReplayDeadLetter(ctx, item.deliveryID, now); replayed || !errors.Is(err, ErrReplayNotAllowed) {
			t.Fatalf("guarded replay %s replayed=%v err=%v", item.deliveryID, replayed, err)
		}
	}
}
