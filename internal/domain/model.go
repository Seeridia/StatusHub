package domain

import (
	"encoding/json"
	"time"
)

// Snapshot is one normalized view returned by an adapter. ObservedAt is
// evidence metadata and must not be included in semantic projections.
type Snapshot struct {
	Source            Source          `json:"source"`
	ResourceKind      ResourceKind    `json:"resource_kind"`
	OverallStatus     ComponentStatus `json:"overall_status"`
	RawOverallStatus  string          `json:"raw_overall_status,omitempty"`
	ComputedStatus    ComponentStatus `json:"computed_status"`
	Components        []Component     `json:"components,omitempty"`
	Incidents         []Incident      `json:"incidents,omitempty"`
	Completeness      Completeness    `json:"completeness"`
	AuthoritativeFor  []ResourceKind  `json:"authoritative_for,omitempty"`
	SourceUpdatedAt   *time.Time      `json:"source_updated_at,omitempty"`
	ObservedAt        time.Time       `json:"observed_at"`
	RawObjectRef      string          `json:"raw_object_ref,omitempty"`
	SchemaVersion     string          `json:"schema_version"`
	AdapterVersion    string          `json:"adapter_version"`
	NormalizerVersion string          `json:"normalizer_version"`
}

func (s Snapshot) IsAuthoritativeFor(kind ResourceKind) bool {
	for _, authoritativeKind := range s.AuthoritativeFor {
		if authoritativeKind == kind {
			return true
		}
	}
	return false
}

func (s Snapshot) CanInferAbsence(kind ResourceKind) bool {
	return s.Completeness == CompletenessComplete && s.IsAuthoritativeFor(kind)
}

type Component struct {
	ID              string            `json:"id"`
	UpstreamID      string            `json:"upstream_id,omitempty"`
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	GroupID         string            `json:"group_id,omitempty"`
	Status          ComponentStatus   `json:"status"`
	RawStatus       string            `json:"raw_status,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	SourceCreatedAt *time.Time        `json:"source_created_at,omitempty"`
	SourceUpdatedAt *time.Time        `json:"source_updated_at,omitempty"`
}

type Incident struct {
	ID              string           `json:"id"`
	UpstreamID      string           `json:"upstream_id,omitempty"`
	Kind            IncidentKind     `json:"kind"`
	Name            string           `json:"name"`
	URL             string           `json:"url,omitempty"`
	Phase           IncidentPhase    `json:"phase"`
	RawPhase        string           `json:"raw_phase,omitempty"`
	Impact          Impact           `json:"impact"`
	RawImpact       string           `json:"raw_impact,omitempty"`
	ComponentIDs    []string         `json:"component_ids,omitempty"`
	Updates         []IncidentUpdate `json:"updates,omitempty"`
	StartedAt       *time.Time       `json:"started_at,omitempty"`
	ScheduledFor    *time.Time       `json:"scheduled_for,omitempty"`
	ScheduledUntil  *time.Time       `json:"scheduled_until,omitempty"`
	ResolvedAt      *time.Time       `json:"resolved_at,omitempty"`
	SourceCreatedAt *time.Time       `json:"source_created_at,omitempty"`
	SourceUpdatedAt *time.Time       `json:"source_updated_at,omitempty"`
	Synthetic       bool             `json:"synthetic,omitempty"`
}

type IncidentUpdate struct {
	ID               string        `json:"id"`
	UpstreamID       string        `json:"upstream_id,omitempty"`
	IncidentID       string        `json:"incident_id"`
	Body             string        `json:"body,omitempty"`
	Phase            IncidentPhase `json:"phase"`
	RawPhase         string        `json:"raw_phase,omitempty"`
	Impact           Impact        `json:"impact"`
	RawImpact        string        `json:"raw_impact,omitempty"`
	ComponentIDs     []string      `json:"component_ids,omitempty"`
	UpstreamSequence *int64        `json:"upstream_sequence,omitempty"`
	SourceCreatedAt  *time.Time    `json:"source_created_at,omitempty"`
	SourceUpdatedAt  *time.Time    `json:"source_updated_at,omitempty"`
}

type FetchRequest struct {
	Target       Target             `json:"target"`
	Source       Source             `json:"source"`
	ResourceKind ResourceKind       `json:"resource_kind"`
	Endpoint     EndpointCapability `json:"endpoint"`
	ETag         string             `json:"etag,omitempty"`
	LastModified *time.Time         `json:"last_modified,omitempty"`
	Cursor       string             `json:"cursor,omitempty"`
	Page         int                `json:"page,omitempty"`
}

type FetchMeta struct {
	Endpoint     string        `json:"endpoint"`
	StatusCode   int           `json:"status_code"`
	ETag         string        `json:"etag,omitempty"`
	LastModified *time.Time    `json:"last_modified,omitempty"`
	ObservedAt   time.Time     `json:"observed_at"`
	NotModified  bool          `json:"not_modified"`
	SchemaHash   string        `json:"schema_hash,omitempty"`
	RawHash      string        `json:"raw_hash,omitempty"`
	RawObjectRef string        `json:"raw_object_ref,omitempty"`
	ContentType  string        `json:"content_type,omitempty"`
	BodyBytes    int64         `json:"body_bytes,omitempty"`
	NextCursor   string        `json:"next_cursor,omitempty"`
	RetryAfter   time.Duration `json:"retry_after,omitempty"`
}

// WebhookRequest preserves the exact body required for signature validation.
// Headers must be treated as transport evidence and excluded from semantic
// hashes.
type WebhookRequest struct {
	Source     Source              `json:"source"`
	ReceivedAt time.Time           `json:"received_at"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
}

// SourceEvent is a normalized statement emitted by an adapter. It is not a
// canonical event: persistence assigns a CanonicalEventID and revision later.
type SourceEvent struct {
	SourceID               string          `json:"source_id"`
	Provider               string          `json:"provider"`
	PageID                 string          `json:"page_id,omitempty"`
	Kind                   EventKind       `json:"kind"`
	EntityKind             EntityKind      `json:"entity_kind"`
	EntityID               string          `json:"entity_id"`
	UpstreamEventID        string          `json:"upstream_event_id,omitempty"`
	SourceUpdatedAt        *time.Time      `json:"source_updated_at,omitempty"`
	UpstreamSequence       *int64          `json:"upstream_sequence,omitempty"`
	ObservedAt             time.Time       `json:"observed_at"`
	NormalizerVersion      string          `json:"normalizer_version"`
	SemanticProjection     json.RawMessage `json:"semantic_projection"`
	SemanticProjectionHash string          `json:"semantic_projection_hash"`
	ObservationKey         ObservationKey  `json:"observation_key,omitempty"`
	SourceEventKey         SourceEventKey  `json:"source_event_key,omitempty"`
}

func (e SourceEvent) Identity() SourceEventIdentity {
	return SourceEventIdentity{
		SourceID:               e.SourceID,
		Provider:               e.Provider,
		PageID:                 e.PageID,
		Kind:                   e.Kind,
		EntityKind:             e.EntityKind,
		EntityID:               e.EntityID,
		UpstreamEventID:        e.UpstreamEventID,
		SourceUpdatedAt:        e.SourceUpdatedAt,
		NormalizerVersion:      e.NormalizerVersion,
		SemanticProjectionHash: e.SemanticProjectionHash,
	}
}
