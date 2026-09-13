package processor

import (
	"context"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/bus"
	"github.com/Seeridia/StatusHub/internal/domain"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type fakeLedger struct {
	event   domain.CanonicalEvent
	message store.OutboxMessage
	result  bool
}

func (f *fakeLedger) InsertEventWithOutbox(_ context.Context, event domain.CanonicalEvent, message store.OutboxMessage) (bool, error) {
	f.event = event
	f.message = message
	return f.result, nil
}

func TestPersistBuildsCompactEnvelope(t *testing.T) {
	ledger := &fakeLedger{result: true}
	processor, err := New(ledger, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ids := []string{"event-uuid", "outbox-uuid"}
	processor.newID = func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}
	event := domain.CanonicalEvent{
		SourceID: "source-1", Kind: domain.EventKindIncidentCreated,
		EntityKind: domain.EntityIncident, EntityID: "incident-upstream",
		AggregateRevision: 1, SourceEventKey: "key", NormalizerVersion: "v1",
		SchemaVersion: "v1", Payload: []byte(`{"large":"canonical payload remains in postgres"}`),
		ObservedAt: time.Now().UTC(),
	}
	result, err := processor.Persist(context.Background(), event)
	if err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	if !result.Inserted || result.EventID != "event-uuid" || result.OutboxID != "outbox-uuid" {
		t.Fatalf("Persist() result = %+v", result)
	}
	envelope, err := bus.DecodeEventEnvelope(ledger.message.Payload)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.EventID != result.EventID || envelope.EntityID != "incident-upstream" {
		t.Fatalf("envelope = %+v", envelope)
	}
	if string(ledger.message.Payload) == string(event.Payload) {
		t.Fatal("outbox unexpectedly contains raw canonical payload instead of compact reference")
	}
}
