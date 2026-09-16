package postgres

import (
	"encoding/json"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type Tenant struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type CreateTenantParams struct {
	ID    string
	Slug  string
	Name  string
	Actor AuditActor
}

type VendorStatus struct {
	Collection        CollectionStatus       `json:"collection"`
	ID                string                 `json:"id"`
	Slug              string                 `json:"slug"`
	Name              string                 `json:"name"`
	CanonicalDomain   string                 `json:"canonical_domain,omitempty"`
	Aliases           []string               `json:"aliases,omitempty"`
	Status            domain.ComponentStatus `json:"status"`
	ActiveIncidents   int                    `json:"active_incidents"`
	VisibleSources    int                    `json:"visible_sources"`
	LastObservedAt    *time.Time             `json:"last_observed_at,omitempty"`
	LastSuccessfulAt  *time.Time             `json:"last_successful_at,omitempty"`
	SourceHealthState string                 `json:"source_health_state"`
}

type SourceView struct {
	Collection           CollectionStatus `json:"collection"`
	ID                   string           `json:"id"`
	TenantID             *string          `json:"tenant_id,omitempty"`
	VendorID             string           `json:"vendor_id"`
	VendorSlug           string           `json:"vendor_slug"`
	VendorName           string           `json:"vendor_name"`
	RequestedURL         string           `json:"requested_url"`
	FinalURL             string           `json:"final_url,omitempty"`
	CanonicalURL         string           `json:"canonical_url"`
	SourceType           string           `json:"source_type"`
	AdapterName          string           `json:"adapter_name,omitempty"`
	AdapterVersion       string           `json:"adapter_version,omitempty"`
	Enabled              bool             `json:"enabled"`
	Ownership            string           `json:"ownership"`
	WorkspaceDisplayName string           `json:"workspace_display_name,omitempty"`
	ArchivedAt           *time.Time       `json:"archived_at,omitempty"`
	ArchiveReason        string           `json:"archive_reason,omitempty"`
	AllowedActions       []string         `json:"allowed_actions"`
	HealthState          string           `json:"health_state"`
	FailureStreak        int              `json:"failure_streak"`
	LastAttemptAt        *time.Time       `json:"last_attempt_at,omitempty"`
	LastSuccessAt        *time.Time       `json:"last_success_at,omitempty"`
	NextPollAt           *time.Time       `json:"next_poll_at,omitempty"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

type IncidentView struct {
	OfficialURL     string               `json:"official_url,omitempty"`
	ID              string               `json:"id"`
	SourceID        string               `json:"source_id"`
	VendorID        string               `json:"vendor_id"`
	VendorSlug      string               `json:"vendor_slug"`
	VendorName      string               `json:"vendor_name"`
	UpstreamID      string               `json:"upstream_id"`
	Name            string               `json:"name"`
	Phase           domain.IncidentPhase `json:"phase"`
	RawPhase        string               `json:"raw_phase"`
	Impact          domain.Impact        `json:"impact"`
	RawImpact       string               `json:"raw_impact,omitempty"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	ResolvedAt      *time.Time           `json:"resolved_at,omitempty"`
	SourceUpdatedAt *time.Time           `json:"source_updated_at,omitempty"`
	ObservedAt      time.Time            `json:"observed_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	Updates         []IncidentUpdateView `json:"updates,omitempty"`
}

type IncidentUpdateView struct {
	ID              string               `json:"id"`
	Phase           domain.IncidentPhase `json:"phase"`
	RawPhase        string               `json:"raw_phase"`
	Body            string               `json:"body"`
	SourceUpdatedAt *time.Time           `json:"source_updated_at,omitempty"`
	ObservedAt      time.Time            `json:"observed_at"`
}

type SubscriptionScopeInput struct {
	VendorID     string `json:"vendor_id,omitempty"`
	ComponentID  string `json:"component_id,omitempty"`
	ComponentKey string `json:"component_key,omitempty"`
	Tag          string `json:"tag,omitempty"`
	EventKind    string `json:"event_kind,omitempty"`
}

type SubscriptionView struct {
	ID           string                   `json:"id"`
	TenantID     string                   `json:"tenant_id"`
	Name         string                   `json:"name"`
	Enabled      bool                     `json:"enabled"`
	PauseReason  string                   `json:"pause_reason,omitempty"`
	Dependencies []ResourceDependency     `json:"pause_dependencies,omitempty"`
	ArchivedAt   *time.Time               `json:"archived_at,omitempty"`
	RuleVersion  int                      `json:"rule_version"`
	Rule         json.RawMessage          `json:"rule"`
	Scopes       []SubscriptionScopeInput `json:"scopes"`
	EndpointIDs  []string                 `json:"endpoint_ids"`
	CreatedAt    time.Time                `json:"created_at"`
	UpdatedAt    time.Time                `json:"updated_at"`
}

type ResourceDependency struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type EndpointView struct {
	ID            string          `json:"id"`
	TenantID      string          `json:"tenant_id"`
	Channel       string          `json:"channel"`
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	ArchivedAt    *time.Time      `json:"archived_at,omitempty"`
	KeyID         string          `json:"key_id"`
	SecretVersion int             `json:"secret_version"`
	HealthState   string          `json:"health_state"`
	RateLimits    json.RawMessage `json:"rate_limit_config"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type EndpointSecret struct {
	EndpointView
	EncryptedConfig []byte
}

type DeliveryAttemptView struct {
	AttemptNumber     int             `json:"attempt_number"`
	SecretVersion     int             `json:"secret_version"`
	Status            string          `json:"status"`
	StartedAt         time.Time       `json:"started_at"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
	HTTPStatus        *int            `json:"http_status,omitempty"`
	ProviderCode      string          `json:"provider_code,omitempty"`
	ProviderMessageID string          `json:"provider_message_id,omitempty"`
	RetryAfter        *time.Time      `json:"retry_after,omitempty"`
	ErrorClass        string          `json:"error_class,omitempty"`
	ErrorSummary      string          `json:"error_summary,omitempty"`
	ResponseMetadata  json.RawMessage `json:"response_metadata"`
}

type DeliveryView struct {
	ID                string                `json:"id"`
	EventID           string                `json:"event_id"`
	SubscriptionID    string                `json:"subscription_id"`
	SubscriptionName  string                `json:"subscription_name"`
	EndpointID        string                `json:"endpoint_id"`
	EndpointName      string                `json:"endpoint_name"`
	Channel           string                `json:"channel"`
	EventKind         domain.EventKind      `json:"event_kind"`
	Status            string                `json:"status"`
	Priority          int16                 `json:"priority"`
	AttemptCount      int                   `json:"attempt_count"`
	ProviderMessageID string                `json:"provider_message_id,omitempty"`
	NextAttemptAt     time.Time             `json:"next_attempt_at"`
	FirstAttemptAt    *time.Time            `json:"first_attempt_at,omitempty"`
	AcceptedAt        *time.Time            `json:"accepted_at,omitempty"`
	DeliveredAt       *time.Time            `json:"delivered_at,omitempty"`
	LastErrorClass    string                `json:"last_error_class,omitempty"`
	LastErrorSummary  string                `json:"last_error_summary,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	Attempts          []DeliveryAttemptView `json:"attempts,omitempty"`
}

type EventView struct {
	ID                string            `json:"id"`
	SourceID          string            `json:"source_id"`
	VendorID          string            `json:"vendor_id"`
	VendorSlug        string            `json:"vendor_slug"`
	Kind              domain.EventKind  `json:"kind"`
	EntityKind        domain.EntityKind `json:"entity_kind"`
	EntityID          string            `json:"entity_id"`
	AggregateRevision int64             `json:"aggregate_revision"`
	SchemaVersion     string            `json:"schema_version"`
	Payload           json.RawMessage   `json:"payload"`
	SourceUpdatedAt   *time.Time        `json:"source_updated_at,omitempty"`
	ObservedAt        time.Time         `json:"observed_at"`
	IngestedAt        time.Time         `json:"ingested_at"`
}

type TimeCursor struct {
	Time time.Time
	ID   string
}

type CreateSourceParams struct {
	AutoVendorSlug   string
	AutoVendorName   string
	ID               string
	TenantID         string
	VendorID         string
	RequestedURL     string
	FinalURL         string
	CanonicalURL     string
	SourceType       string
	AdapterName      string
	AdapterVersion   string
	ActiveRegion     string
	Capabilities     domain.Capabilities
	DisplayName      string
	ReplacesSourceID string
	Actor            AuditActor
}

type UpdateWorkspaceSourceParams struct {
	TenantID    string
	SourceID    string
	DisplayName *string
	Enabled     *bool
	Actor       AuditActor
}

type SourceCatalogItem struct {
	ID           string `json:"id"`
	VendorID     string `json:"vendor_id"`
	VendorSlug   string `json:"vendor_slug"`
	VendorName   string `json:"vendor_name"`
	CanonicalURL string `json:"canonical_url"`
	HealthState  string `json:"health_state"`
	Added        bool   `json:"added"`
}

type CreateSubscriptionParams struct {
	ID          string
	TenantID    string
	Name        string
	Enabled     bool
	Rule        json.RawMessage
	Scopes      []SubscriptionScopeInput
	EndpointIDs []string
	Actor       AuditActor
}

type UpdateSubscriptionParams struct {
	CreateSubscriptionParams
	ExpectedRuleVersion int
}

type CreateEndpointParams struct {
	ID              string
	TenantID        string
	Channel         string
	Name            string
	Enabled         bool
	EncryptedConfig []byte
	KeyID           string
	SecretVersion   int
	RateLimits      json.RawMessage
	Actor           AuditActor
}

type IdempotencyRecord struct {
	TenantID       string
	Key            string
	Method         string
	Route          string
	RequestHash    []byte
	State          string
	ResourceID     string
	ResponseStatus int
	ResponseBody   json.RawMessage
	LockedUntil    time.Time
	ExpiresAt      time.Time
}

type EndpointTestJob struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenant_id"`
	EndpointID        string     `json:"endpoint_id"`
	Status            string     `json:"status"`
	AttemptCount      int        `json:"attempt_count"`
	ProviderMessageID string     `json:"provider_message_id,omitempty"`
	HTTPStatus        *int       `json:"http_status,omitempty"`
	ErrorClass        string     `json:"error_class,omitempty"`
	ErrorSummary      string     `json:"error_summary,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type EndpointTestLease struct {
	EndpointTestJob
	LeaseToken      string
	Channel         string
	KeyID           string
	SecretVersion   int
	EncryptedConfig []byte
}

type CompleteEndpointTestParams struct {
	ID                string
	LeaseToken        string
	Succeeded         bool
	ProviderMessageID string
	HTTPStatus        int
	ErrorClass        string
	ErrorSummary      string
}
