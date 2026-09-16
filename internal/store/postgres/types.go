package postgres

import (
	"encoding/json"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/reconcile"
)

type DeliveryLane string

const (
	DeliveryLaneCritical DeliveryLane = "critical"
	DeliveryLaneRetry    DeliveryLane = "retry"
	DeliveryLaneBulk     DeliveryLane = "bulk"
)

func (lane DeliveryLane) Valid() bool {
	switch lane {
	case DeliveryLaneCritical, DeliveryLaneRetry, DeliveryLaneBulk:
		return true
	default:
		return false
	}
}

// SourceLease is a due source plus the fencing token required to complete or
// fail its poll. Nullable database columns are represented by pointers.
type SourceLease struct {
	ID             string
	TenantID       *string
	VendorID       string
	RequestedURL   string
	FinalURL       *string
	CanonicalURL   string
	SourceType     string
	AdapterName    *string
	AdapterVersion *string
	HealthState    string
	NextPollAt     time.Time
	LeaseOwner     string
	LeaseToken     string
	LeaseUntil     time.Time
	FailureStreak  int
	LastAttemptAt  *time.Time
	LastSuccessAt  *time.Time
	LastCheckpoint *string
	ActiveRegion   string
	OwnershipEpoch int64
}

type CompletePollParams struct {
	SourceID       string
	LeaseToken     string
	NextPollAt     time.Time
	Checkpoint     *string
	HealthState    string
	SuccessfulAt   *time.Time
	ActiveRegion   string
	OwnershipEpoch int64
}

type FailPollParams struct {
	FailureCode    string
	SourceID       string
	LeaseToken     string
	NextPollAt     time.Time
	HealthState    string
	ActiveRegion   string
	OwnershipEpoch int64
}

// OutboxMessage is inserted in the same transaction as its canonical event.
// ID may be empty, in which case PostgreSQL allocates a UUID. A zero
// AvailableAt means the transaction timestamp.
type OutboxMessage struct {
	ID          string
	Subject     string
	Payload     json.RawMessage
	AvailableAt time.Time
}

// EventWrite is one canonical event and the bus reference that must become
// visible atomically with it.
type EventWrite struct {
	Event  domain.CanonicalEvent
	Outbox OutboxMessage
}

// CommitPollParams atomically advances a leased source checkpoint and inserts
// every newly reconciled event/outbox pair. Expected lease ownership is the
// fencing condition for the complete commit.
type CommitPollParams struct {
	// State projects the accepted reconciliation state into console read models.
	State          *reconcile.State
	SourceID       string
	LeaseToken     string
	NextPollAt     time.Time
	Checkpoint     string
	HealthState    string
	SuccessfulAt   time.Time
	Writes         []EventWrite
	ActiveRegion   string
	OwnershipEpoch int64
}

type CommitPollResult struct {
	InsertedEvents int
}

// OutboxLease is a publication attempt. Attempts is incremented when the row
// is claimed, not when it is completed.
type OutboxLease struct {
	ID          string
	EventID     string
	Subject     string
	Payload     json.RawMessage
	AvailableAt time.Time
	Attempts    int
	LastError   *string
	CreatedAt   time.Time
	LeaseOwner  string
	LeaseToken  string
	LeaseUntil  time.Time
}

type FailOutboxParams struct {
	ID          string
	LeaseToken  string
	AvailableAt time.Time
	LastError   string
}

// Delivery identifies one logical notification. The database uniqueness key
// is EventID + SubscriptionID + EndpointID + TemplateVersion, so a redelivered
// bus message can safely call EnsureDelivery again.
type Delivery struct {
	ID              string
	EventID         string
	SubscriptionID  string
	EndpointID      string
	TemplateVersion int
	Status          string
	Priority        int16
	NextAttemptAt   time.Time
	ExpiresAt       *time.Time
}

type FanoutShardLease struct {
	PlanID               string
	EventID              string
	ShardNumber          int
	ShardCount           int
	CursorSubscriptionID *string
	LeaseOwner           string
	LeaseToken           string
	LeaseUntil           time.Time
}

type FanoutCandidate struct {
	SubscriptionID string
	EndpointID     string
	RuleVersion    int
	Rule           json.RawMessage
}

type FanoutDelivery struct {
	ID              string    `json:"id"`
	SubscriptionID  string    `json:"subscription_id"`
	EndpointID      string    `json:"endpoint_id"`
	RuleVersion     int       `json:"rule_version"`
	TemplateVersion int       `json:"template_version"`
	Priority        int16     `json:"priority"`
	EligibleAt      time.Time `json:"eligible_at"`
}

type FanoutEvent struct {
	ID         string
	Kind       domain.EventKind
	Payload    json.RawMessage
	ObservedAt time.Time
	IngestedAt time.Time
}

type CommitFanoutShardParams struct {
	PlanID               string
	ShardNumber          int
	LeaseToken           string
	CursorSubscriptionID *string
	Completed            bool
	Deliveries           []FanoutDelivery
}

type ProviderCallback struct {
	ID                string
	EndpointID        string
	DeliveryID        *string
	ProviderEventID   string
	ProviderMessageID string
	ReceivedAt        time.Time
	RawBodySHA256     []byte
	Payload           json.RawMessage
	DeliveryStatus    string
}

type DeliveryLease struct {
	TenantID           string
	VendorID           string
	ServiceName        string
	AffectedServices   []string
	IncidentID         string
	ID                 string
	EventID            string
	SubscriptionID     string
	EndpointID         string
	Channel            string
	EncryptedConfig    []byte
	KeyID              string
	SecretVersion      int
	AttemptNumber      int
	LeaseToken         string
	LeaseUntil         time.Time
	EligibleAt         time.Time
	EventSource        string
	EventKind          domain.EventKind
	EventEntityID      string
	EventRevision      uint64
	EventSchemaVersion string
	EventPayload       json.RawMessage
	EventObservedAt    time.Time
	Lane               DeliveryLane
}

type CompleteDeliveryParams struct {
	DeliveryID        string
	LeaseToken        string
	AttemptNumber     int
	Status            string
	ProviderMessageID string
	HTTPStatus        int
	ProviderCode      string
	ErrorClass        string
	ErrorSummary      string
	RetryAt           *time.Time
	DisableEndpoint   bool
	FinishedAt        time.Time
	DeadLetterReason  string
	AgentID           string
}

type DeadLetter struct {
	ID                  string           `json:"id"`
	EventID             string           `json:"event_id"`
	SubscriptionID      string           `json:"subscription_id"`
	EndpointID          string           `json:"endpoint_id"`
	Channel             string           `json:"channel"`
	EventKind           domain.EventKind `json:"event_kind"`
	Subject             string           `json:"subject"`
	AttemptCount        int              `json:"attempt_count"`
	ReplayCount         int              `json:"replay_count"`
	Reason              string           `json:"reason"`
	DeadLetteredAt      time.Time        `json:"dead_lettered_at"`
	EndpointEnabled     bool             `json:"endpoint_enabled"`
	SubscriptionEnabled bool             `json:"subscription_enabled"`
}

type EndpointCallbackConfig struct {
	ID              string
	Channel         string
	EncryptedConfig []byte
	Enabled         bool
}

type AWSAccountConnector struct {
	ID                string
	SourceID          string
	TenantID          string
	ExternalAccountID string
	SNSTopicARN       string
	AllowedRegions    []string
	AllowedServices   []string
	Enabled           bool
}

type CreateAWSAccountConnectorParams struct {
	ID                string
	SourceID          string
	TenantID          string
	ExternalAccountID string
	SNSTopicARN       string
	AllowedRegions    []string
	AllowedServices   []string
}

type CommitConnectorEventParams struct {
	Event        domain.CanonicalEvent
	Subject      string
	SemanticHash string
}

type ConnectorCommitResult struct {
	EventID  domain.CanonicalEventID
	Revision uint64
	Inserted bool
}

type PrivateAgent struct {
	ID         string
	TenantID   string
	Name       string
	Enabled    bool
	LastSeenAt *time.Time
	Version    string
}

type PrivateAgentCredential struct {
	Agent PrivateAgent
	Token string
}
