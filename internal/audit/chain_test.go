package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestChainDetectsMutationAndSequenceGaps(t *testing.T) {
	when := time.Date(2026, 9, 10, 8, 0, 0, 123, time.UTC)
	first, firstHash, err := NewEvent("tenant-1", 1, ZeroHash, AppendInput{
		OccurredAt: when, ActorType: "user", ActorID: "subject-1", Action: "endpoint.create",
		ResourceType: "endpoint", ResourceID: "endpoint-1", Outcome: "success",
		Metadata: json.RawMessage(`{"z":1,"a":"value"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := NewEvent("tenant-1", 2, firstHash, AppendInput{
		OccurredAt: when.Add(time.Second), ActorType: "service_account", ActorID: "sa-1",
		Action: "delivery.replay", ResourceType: "delivery", ResourceID: "delivery-1",
		Outcome: "success", Metadata: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify([]Event{first, second}); err != nil {
		t.Fatal(err)
	}
	if string(first.Metadata) != `{"a":"value","z":1}` {
		t.Fatalf("metadata not canonical: %s", first.Metadata)
	}

	mutated := second
	mutated.Action = "delivery.delete"
	if err := Verify([]Event{first, mutated}); err == nil || !strings.Contains(err.Error(), "event hash") {
		t.Fatalf("mutation error=%v", err)
	}
	if err := Verify([]Event{second}); err == nil || !strings.Contains(err.Error(), "sequence gap") {
		t.Fatalf("sequence error=%v", err)
	}
}

func TestCanonicalJSONRejectsInvalidInput(t *testing.T) {
	if _, err := CanonicalJSON(json.RawMessage(`{"broken":`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
