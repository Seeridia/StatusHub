package bus

import (
	"reflect"
	"testing"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

func TestEventEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()
	want := EventEnvelope{
		Version: EventEnvelopeVersion, EventID: "event-1", SourceID: "source-1",
		Kind: domain.EventKindIncidentUpdated, EntityKind: domain.EntityIncident,
		EntityID: "upstream-incident", AggregateRevision: 2, SchemaVersion: "v1",
	}
	data, err := want.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got, err := DecodeEventEnvelope(data)
	if err != nil {
		t.Fatalf("DecodeEventEnvelope() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded = %#v, want %#v", got, want)
	}
}
