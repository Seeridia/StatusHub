package domain

import (
	"encoding/json"
	"time"
)

// CanonicalEvent is a durable, product-level change produced by
// reconciliation. ID is intentionally optional before the event ledger has
// inserted the event; the ledger allocates it once and must keep it stable for
// every outbox publication retry.
type CanonicalEvent struct {
	ID                CanonicalEventID `json:"id,omitempty"`
	SourceID          string           `json:"source_id"`
	Provider          string           `json:"provider"`
	PageID            string           `json:"page_id,omitempty"`
	Kind              EventKind        `json:"kind"`
	EntityKind        EntityKind       `json:"entity_kind"`
	EntityID          string           `json:"entity_id"`
	AggregateRevision uint64           `json:"aggregate_revision"`
	SourceEventKey    SourceEventKey   `json:"source_event_key"`
	NormalizerVersion string           `json:"normalizer_version"`
	SchemaVersion     string           `json:"schema_version"`
	Payload           json.RawMessage  `json:"payload"`
	SourceUpdatedAt   *time.Time       `json:"source_updated_at,omitempty"`
	ObservedAt        time.Time        `json:"observed_at"`
}
