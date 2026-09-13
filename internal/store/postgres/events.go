package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const insertCanonicalEventSQL = `
INSERT INTO canonical_events (
    id,
    source_id,
    entity_type,
    entity_id,
    event_kind,
    aggregate_revision,
    source_event_key,
    normalizer_version,
    canonical_schema_version,
    canonical_payload,
    source_updated_at,
    observed_at
)
VALUES (
    COALESCE($1::uuid, gen_random_uuid()),
    $2,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9,
    $10,
    $11,
    $12
)
ON CONFLICT (source_id, source_event_key) DO NOTHING
RETURNING id`

const insertOutboxSQL = `
INSERT INTO outbox (
    id,
    event_id,
    subject,
    payload,
    available_at
)
VALUES (
    COALESCE($1::uuid, gen_random_uuid()),
    $2,
    $3,
    $4,
    COALESCE($5, statement_timestamp())
)`

// InsertEventWithOutbox atomically inserts an immutable canonical event and
// its publication message. The database unique constraint on
// (source_id, source_event_key) is the concurrency authority: duplicate
// logical events return inserted=false and never create a second outbox row.
func (s *Store) InsertEventWithOutbox(
	ctx context.Context,
	event domain.CanonicalEvent,
	message OutboxMessage,
) (inserted bool, err error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return false, err
	}
	if err := validateOutboxMessage(message); err != nil {
		return false, err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("postgres store: insert event with outbox: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID string
	var requestedEventID *string
	if event.ID != "" {
		value := string(event.ID)
		requestedEventID = &value
	}
	entityID := nullableString(event.EntityID)
	if err := tx.QueryRow(
		ctx,
		insertCanonicalEventSQL,
		requestedEventID,
		event.SourceID,
		string(event.EntityKind),
		entityID,
		string(event.Kind),
		int64(event.AggregateRevision),
		string(event.SourceEventKey),
		event.NormalizerVersion,
		event.SchemaVersion,
		event.Payload,
		event.SourceUpdatedAt,
		event.ObservedAt,
	).Scan(&eventID); err != nil {
		if isNoRows(err) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return false, fmt.Errorf("postgres store: insert duplicate event: commit: %w", commitErr)
			}
			return false, nil
		}
		return false, fmt.Errorf("postgres store: insert canonical event: %w", err)
	}

	var messageID *string
	if strings.TrimSpace(message.ID) != "" {
		messageID = &message.ID
	}
	var availableAt any
	if !message.AvailableAt.IsZero() {
		availableAt = message.AvailableAt
	}
	if _, err := tx.Exec(
		ctx,
		insertOutboxSQL,
		messageID,
		eventID,
		message.Subject,
		message.Payload,
		availableAt,
	); err != nil {
		return false, fmt.Errorf("postgres store: insert outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres store: insert event with outbox: commit: %w", err)
	}
	return true, nil
}

func validateCanonicalEvent(event domain.CanonicalEvent) error {
	if err := required(event.SourceID, "event source ID"); err != nil {
		return err
	}
	if !event.EntityKind.Valid() {
		return invalid("event entity kind is invalid")
	}
	if err := required(event.EntityID, "event entity ID"); err != nil {
		return err
	}
	if !event.Kind.Valid() {
		return invalid("event kind is invalid")
	}
	if event.AggregateRevision == 0 || event.AggregateRevision > math.MaxInt64 {
		return invalid("event aggregate revision must fit a positive bigint")
	}
	if err := required(string(event.SourceEventKey), "source event key"); err != nil {
		return err
	}
	if err := required(event.NormalizerVersion, "normalizer version"); err != nil {
		return err
	}
	if err := required(event.SchemaVersion, "canonical schema version"); err != nil {
		return err
	}
	if len(event.Payload) == 0 || !json.Valid(event.Payload) {
		return invalid("canonical event payload must be valid JSON")
	}
	if event.ObservedAt.IsZero() {
		return invalid("event observed time is required")
	}
	return nil
}

func validateOutboxMessage(message OutboxMessage) error {
	if err := required(message.Subject, "outbox subject"); err != nil {
		return err
	}
	if len(message.Payload) == 0 || !json.Valid(message.Payload) {
		return invalid("outbox payload must be valid JSON")
	}
	return nil
}

func nullableString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
