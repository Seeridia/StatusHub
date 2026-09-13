package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

func TestIntegrationCommitPollAtomicallyFencesCheckpointEventAndOutbox(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(-time.Minute))

	leases, err := database.store.AcquireDueSources(ctx, "collector-a", 1, time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("acquire source: leases=%d err=%v", len(leases), err)
	}
	event := integrationEvent("leased-logical-event", "upstream-incident-not-a-uuid")
	event.ID = domain.CanonicalEventID(integrationUUID(901))
	write := EventWrite{
		Event: event,
		Outbox: OutboxMessage{
			ID: integrationUUID(902), Subject: "statushub.events.normal",
			Payload: []byte(`{"version":"1","event_id":"` + string(event.ID) + `"}`),
		},
	}
	first, err := database.store.CommitPoll(ctx, CommitPollParams{
		SourceID: integrationSourceID, LeaseToken: leases[0].LeaseToken,
		NextPollAt: time.Now().Add(time.Minute), Checkpoint: `{"version":1}`,
		HealthState: "healthy", SuccessfulAt: time.Now(), Writes: []EventWrite{write},
	})
	if err != nil || first.InsertedEvents != 1 {
		t.Fatalf("commit first poll = %+v err=%v", first, err)
	}
	assertTableCount(t, database.pool, "canonical_events", 1)
	assertTableCount(t, database.pool, "outbox", 1)

	if _, err := database.pool.Exec(ctx, `UPDATE sources SET next_poll_at = now() - interval '1 second' WHERE id = $1`, integrationSourceID); err != nil {
		t.Fatalf("make source due: %v", err)
	}
	replacement, err := database.store.AcquireDueSources(ctx, "collector-b", 1, time.Minute)
	if err != nil || len(replacement) != 1 {
		t.Fatalf("acquire replacement lease: leases=%d err=%v", len(replacement), err)
	}

	staleEvent := integrationEvent("stale-logical-event", "upstream-stale")
	staleEvent.ID = domain.CanonicalEventID(integrationUUID(903))
	_, err = database.store.CommitPoll(ctx, CommitPollParams{
		SourceID: integrationSourceID, LeaseToken: leases[0].LeaseToken,
		NextPollAt: time.Now().Add(time.Minute), Checkpoint: `{"stale":true}`,
		Writes: []EventWrite{{
			Event:  staleEvent,
			Outbox: OutboxMessage{ID: integrationUUID(904), Subject: "statushub.events.normal", Payload: []byte(`{}`)},
		}},
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale CommitPoll error = %v, want ErrLeaseLost", err)
	}
	assertTableCount(t, database.pool, "canonical_events", 1)
	assertTableCount(t, database.pool, "outbox", 1)

	duplicate := write
	duplicate.Outbox.ID = integrationUUID(905)
	second, err := database.store.CommitPoll(ctx, CommitPollParams{
		SourceID: integrationSourceID, LeaseToken: replacement[0].LeaseToken,
		NextPollAt: time.Now().Add(time.Minute), Checkpoint: `{"version":2}`,
		HealthState: "healthy", Writes: []EventWrite{duplicate},
	})
	if err != nil || second.InsertedEvents != 0 {
		t.Fatalf("commit duplicate poll = %+v err=%v", second, err)
	}
	assertTableCount(t, database.pool, "canonical_events", 1)
	assertTableCount(t, database.pool, "outbox", 1)
	var checkpoint string
	if err := database.pool.QueryRow(ctx, `SELECT last_checkpoint FROM sources WHERE id = $1`, integrationSourceID).Scan(&checkpoint); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if checkpoint != `{"version":2}` {
		t.Fatalf("checkpoint = %q", checkpoint)
	}
}
