package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const awsAccountHealthProvider = "aws-account-health"

func (s *Store) CreateAWSAccountConnector(ctx context.Context, params CreateAWSAccountConnectorParams) (AWSAccountConnector, error) {
	if err := s.ready(); err != nil {
		return AWSAccountConnector{}, err
	}
	if err := validateAWSConnector(params); err != nil {
		return AWSAccountConnector{}, err
	}
	if params.ID == "" {
		params.ID = uuid.NewString()
	}
	if params.SourceID == "" {
		params.SourceID = uuid.NewString()
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: create AWS connector: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var vendorID string
	if err := tx.QueryRow(ctx, `SELECT id FROM vendors WHERE slug='aws'`).Scan(&vendorID); err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: create AWS connector: AWS vendor is not configured: %w", err)
	}
	canonicalURL := "aws-health://" + params.ExternalAccountID
	if _, err := tx.Exec(ctx, `
INSERT INTO sources (
    id, tenant_id, vendor_id, requested_url, canonical_url, source_type,
    adapter_name, adapter_version, health_state
) VALUES ($1,$2,$3,$4,$4,'account_connector',$5,'1','healthy')`,
		params.SourceID, params.TenantID, vendorID, canonicalURL, awsAccountHealthProvider); err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: create AWS connector source: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO account_connectors (
    id, source_id, tenant_id, provider, external_account_id, sns_topic_arn,
    allowed_regions, allowed_services
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, params.ID, params.SourceID, params.TenantID,
		awsAccountHealthProvider, params.ExternalAccountID, params.SNSTopicARN,
		normalizeAllowlist(params.AllowedRegions), normalizeAllowlist(params.AllowedServices)); err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: create AWS connector: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: create AWS connector: commit: %w", err)
	}
	return s.LoadAWSAccountConnector(ctx, params.ID)
}

func (s *Store) LoadAWSAccountConnector(ctx context.Context, connectorID string) (AWSAccountConnector, error) {
	if err := s.ready(); err != nil {
		return AWSAccountConnector{}, err
	}
	if _, err := uuid.Parse(connectorID); err != nil {
		return AWSAccountConnector{}, invalid("AWS connector ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: load AWS connector: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var connector AWSAccountConnector
	err = tx.QueryRow(ctx, `
SELECT ac.id, ac.source_id, ac.tenant_id, ac.external_account_id,
       ac.sns_topic_arn, ac.allowed_regions, ac.allowed_services,
       ac.enabled AND src.enabled
FROM account_connectors ac
JOIN sources src ON src.id=ac.source_id AND src.tenant_id=ac.tenant_id
WHERE ac.id=$1 AND ac.provider=$2`, connectorID, awsAccountHealthProvider).Scan(
		&connector.ID, &connector.SourceID, &connector.TenantID, &connector.ExternalAccountID,
		&connector.SNSTopicARN, &connector.AllowedRegions, &connector.AllowedServices, &connector.Enabled,
	)
	if err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: load AWS connector: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AWSAccountConnector{}, fmt.Errorf("postgres store: load AWS connector: commit: %w", err)
	}
	return connector, nil
}

// CommitConnectorEvent serializes updates per source/entity, allocates a
// monotonic ingest revision, and atomically writes the canonical event and
// outbox reference. Older provider timestamps remain in the audit ledger but
// cannot replace the connector's latest-state projection.
func (s *Store) CommitConnectorEvent(ctx context.Context, params CommitConnectorEventParams) (ConnectorCommitResult, error) {
	if err := s.ready(); err != nil {
		return ConnectorCommitResult{}, err
	}
	if strings.TrimSpace(params.SemanticHash) == "" || strings.TrimSpace(params.Subject) == "" {
		return ConnectorCommitResult{}, invalid("connector semantic hash and subject are required")
	}
	if err := bus.ValidateSubject(params.Subject); err != nil {
		return ConnectorCommitResult{}, invalid("connector outbox subject is invalid")
	}
	event := params.Event
	if err := validateConnectorEvent(event); err != nil {
		return ConnectorCommitResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: commit connector event: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO connector_entity_states(source_id,entity_id,entity_type)
VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, event.SourceID, event.EntityID, string(event.EntityKind)); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: initialize connector state: %w", err)
	}
	var revision int64
	var latestSourceUpdatedAt *time.Time
	var semanticHash *string
	if err := tx.QueryRow(ctx, `
SELECT revision,latest_source_updated_at,semantic_hash
FROM connector_entity_states WHERE source_id=$1 AND entity_id=$2 FOR UPDATE`, event.SourceID, event.EntityID).Scan(
		&revision, &latestSourceUpdatedAt, &semanticHash,
	); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: lock connector state: %w", err)
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
    SELECT 1 FROM canonical_events WHERE source_id=$1 AND source_event_key=$2
)`, event.SourceID, string(event.SourceEventKey)).Scan(&duplicate); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: check connector duplicate: %w", err)
	}
	if duplicate || (semanticHash != nil && *semanticHash == params.SemanticHash && sameInstant(latestSourceUpdatedAt, event.SourceUpdatedAt)) {
		if err := tx.Commit(ctx); err != nil {
			return ConnectorCommitResult{}, fmt.Errorf("postgres store: commit connector duplicate: %w", err)
		}
		return ConnectorCommitResult{Revision: uint64(revision)}, nil
	}
	revision++
	if revision > 1 && event.Kind == domain.EventKindIncidentCreated {
		event.Kind = domain.EventKindIncidentUpdated
	}
	event.AggregateRevision = uint64(revision)
	if event.ID == "" {
		event.ID = domain.CanonicalEventID(uuid.NewString())
	}
	if err := validateCanonicalEvent(event); err != nil {
		return ConnectorCommitResult{}, err
	}
	envelope, err := (bus.EventEnvelope{
		Version: bus.EventEnvelopeVersion, EventID: event.ID, SourceID: event.SourceID,
		Kind: event.Kind, EntityKind: event.EntityKind, EntityID: event.EntityID,
		AggregateRevision: event.AggregateRevision, SchemaVersion: event.SchemaVersion,
	}).Marshal()
	if err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: build connector envelope: %w", err)
	}
	var insertedID string
	entityID := nullableString(event.EntityID)
	err = tx.QueryRow(ctx, insertCanonicalEventSQL, string(event.ID), event.SourceID, string(event.EntityKind), entityID,
		string(event.Kind), int64(event.AggregateRevision), string(event.SourceEventKey), event.NormalizerVersion,
		event.SchemaVersion, event.Payload, event.SourceUpdatedAt, event.ObservedAt).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return ConnectorCommitResult{}, fmt.Errorf("postgres store: commit connector duplicate: %w", err)
		}
		return ConnectorCommitResult{Revision: uint64(revision - 1)}, nil
	}
	if err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: insert connector event: %w", err)
	}
	if _, err := tx.Exec(ctx, insertOutboxSQL, uuid.NewString(), insertedID, params.Subject, envelope, nil); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: insert connector outbox: %w", err)
	}
	isCurrent := latestSourceUpdatedAt == nil || event.SourceUpdatedAt == nil || !event.SourceUpdatedAt.Before(*latestSourceUpdatedAt)
	if _, err := tx.Exec(ctx, `
UPDATE connector_entity_states
SET revision=$3,
    entity_type=CASE WHEN $4 THEN $5 ELSE entity_type END,
    latest_source_updated_at=CASE WHEN $4 THEN $6 ELSE latest_source_updated_at END,
    semantic_hash=CASE WHEN $4 THEN $7 ELSE semantic_hash END,
    last_event_kind=CASE WHEN $4 THEN $8 ELSE last_event_kind END,
    latest_payload=CASE WHEN $4 THEN $9 ELSE latest_payload END,
    updated_at=statement_timestamp()
WHERE source_id=$1 AND entity_id=$2`, event.SourceID, event.EntityID, revision, isCurrent,
		string(event.EntityKind), event.SourceUpdatedAt, params.SemanticHash, string(event.Kind), event.Payload); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: update connector state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE account_connectors SET last_event_at=statement_timestamp(),updated_at=statement_timestamp()
WHERE source_id=$1`, event.SourceID); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: update connector heartbeat: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ConnectorCommitResult{}, fmt.Errorf("postgres store: commit connector event: commit: %w", err)
	}
	return ConnectorCommitResult{EventID: event.ID, Revision: uint64(revision), Inserted: true}, nil
}

func validateAWSConnector(params CreateAWSAccountConnectorParams) error {
	if _, err := uuid.Parse(params.TenantID); err != nil {
		return invalid("AWS connector tenant ID must be a UUID")
	}
	if params.ID != "" {
		if _, err := uuid.Parse(params.ID); err != nil {
			return invalid("AWS connector ID must be a UUID")
		}
	}
	if params.SourceID != "" {
		if _, err := uuid.Parse(params.SourceID); err != nil {
			return invalid("AWS connector source ID must be a UUID")
		}
	}
	if len(params.ExternalAccountID) != 12 {
		return invalid("AWS account ID must contain 12 digits")
	}
	for _, character := range params.ExternalAccountID {
		if character < '0' || character > '9' {
			return invalid("AWS account ID must contain 12 digits")
		}
	}
	parts := strings.Split(params.SNSTopicARN, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[2] != "sns" || parts[3] == "" || parts[4] != params.ExternalAccountID || parts[5] == "" {
		return invalid("SNS topic ARN must belong to the configured AWS account")
	}
	switch parts[1] {
	case "aws", "aws-cn", "aws-us-gov":
	default:
		return invalid("SNS topic ARN partition is unsupported")
	}
	for _, values := range [][]string{params.AllowedRegions, params.AllowedServices} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n") {
				return invalid("connector allowlist contains an invalid value")
			}
		}
	}
	return nil
}

func validateConnectorEvent(event domain.CanonicalEvent) error {
	if err := required(event.SourceID, "connector event source ID"); err != nil {
		return err
	}
	if !event.EntityKind.Valid() || !event.Kind.Valid() || strings.TrimSpace(event.EntityID) == "" {
		return invalid("connector event identity is invalid")
	}
	if strings.TrimSpace(string(event.SourceEventKey)) == "" || strings.TrimSpace(event.NormalizerVersion) == "" || strings.TrimSpace(event.SchemaVersion) == "" {
		return invalid("connector event version identity is invalid")
	}
	if len(event.Payload) == 0 || !json.Valid(event.Payload) || event.ObservedAt.IsZero() {
		return invalid("connector event payload and observation time are required")
	}
	return nil
}

func normalizeAllowlist(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if _, found := seen[value]; value == "" || found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sameInstant(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
