// Package reconcile turns normalized snapshots into monotonic aggregate state
// and canonical change events. It is deliberately independent of a database:
// callers load State, Apply a snapshot, then persist the returned state and
// events atomically with compare-and-swap.
package reconcile

import (
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

// State is the complete source-scoped reconciliation checkpoint.
// AppliedObservations makes retries of an already accepted observation a
// no-op. A persistence implementation may compact old entries once its own
// observation-key uniqueness constraint makes replay impossible.
type State struct {
	SourceID            string                    `json:"source_id"`
	BaselineEstablished bool                      `json:"baseline_established"`
	Components          map[string]ComponentState `json:"components,omitempty"`
	Incidents           map[string]IncidentState  `json:"incidents,omitempty"`
	AppliedObservations map[string]bool           `json:"applied_observations,omitempty"`
}

// ComponentState is the current component aggregate. Revision starts at one
// and advances for every semantic aggregate change, even when that change is
// metadata-only and therefore does not emit component.status_changed.
type ComponentState struct {
	Component    domain.Component `json:"component"`
	Revision     uint64           `json:"revision"`
	SemanticHash string           `json:"semantic_hash"`
	Watermark    Watermark        `json:"watermark"`
}

// IncidentState is the current incident aggregate. MissingCount is only
// advanced by complete snapshots authoritative for the unresolved-incident
// set (or for the complete incident set).
type IncidentState struct {
	Incident       domain.Incident `json:"incident"`
	Revision       uint64          `json:"revision"`
	SemanticHash   string          `json:"semantic_hash"`
	Watermark      Watermark       `json:"watermark"`
	LastObservedAt time.Time       `json:"last_observed_at"`
	MissingCount   uint8           `json:"missing_count"`
}

// Watermark orders evidence for one aggregate. SourceUpdatedAt is preferred
// when both candidates provide it; ObservedAt breaks ties and orders sources
// that do not expose an upstream timestamp.
type Watermark struct {
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
	ObservedAt      time.Time  `json:"observed_at"`
}

type Result struct {
	State               State                   `json:"state"`
	Events              []domain.CanonicalEvent `json:"events,omitempty"`
	BaselineEstablished bool                    `json:"baseline_established"`
	Changed             bool                    `json:"changed"`
	Duplicate           bool                    `json:"duplicate"`
}
