package bus

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const EventEnvelopeVersion = "1"

// EventEnvelope is intentionally compact. Consumers load full canonical data
// from PostgreSQL by EventID; upstream raw bodies never transit the broker.
type EventEnvelope struct {
	Version           string                  `json:"version"`
	EventID           domain.CanonicalEventID `json:"event_id"`
	SourceID          string                  `json:"source_id"`
	Kind              domain.EventKind        `json:"kind"`
	EntityKind        domain.EntityKind       `json:"entity_kind"`
	EntityID          string                  `json:"entity_id"`
	AggregateRevision uint64                  `json:"aggregate_revision"`
	SchemaVersion     string                  `json:"schema_version"`
	RawObjectRef      string                  `json:"raw_object_ref,omitempty"`
}

func (e EventEnvelope) Validate() error {
	if e.Version != EventEnvelopeVersion {
		return errors.New("bus event envelope version is unsupported")
	}
	if strings.TrimSpace(string(e.EventID)) == "" || strings.TrimSpace(e.SourceID) == "" {
		return errors.New("bus event and source IDs are required")
	}
	if !e.Kind.Valid() || !e.EntityKind.Valid() || strings.TrimSpace(e.EntityID) == "" {
		return errors.New("bus event identity is invalid")
	}
	if e.AggregateRevision == 0 || strings.TrimSpace(e.SchemaVersion) == "" {
		return errors.New("bus event revision and schema version are required")
	}
	return nil
}

func (e EventEnvelope) Marshal() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

func DecodeEventEnvelope(data []byte) (EventEnvelope, error) {
	var envelope EventEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return EventEnvelope{}, err
	}
	if err := envelope.Validate(); err != nil {
		return EventEnvelope{}, err
	}
	return envelope, nil
}
