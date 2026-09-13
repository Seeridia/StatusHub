package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const lockSourceLeaseSQL = `
SELECT id
FROM sources
WHERE id = $1
  AND lease_token = $2
  AND lease_until > statement_timestamp()
FOR UPDATE`

const lockRegionalSourceLeaseSQL = `
SELECT source.id
FROM sources source
JOIN source_ownership ownership ON ownership.source_id=source.id
WHERE source.id=$1 AND source.lease_token=$2 AND source.lease_until>statement_timestamp()
  AND ownership.active_region=$3 AND ownership.epoch=$4
FOR UPDATE OF source,ownership`

// CommitPoll makes the reconciliation checkpoint and every newly inserted
// canonical event/outbox pair visible in one transaction. If the source lease
// has been replaced, none of the writes are committed.
func (s *Store) CommitPoll(ctx context.Context, params CommitPollParams) (CommitPollResult, error) {
	if err := s.ready(); err != nil {
		return CommitPollResult{}, err
	}
	if err := validatePollCAS(params.SourceID, params.LeaseToken, params.NextPollAt); err != nil {
		return CommitPollResult{}, err
	}
	if strings.TrimSpace(params.Checkpoint) == "" {
		return CommitPollResult{}, invalid("poll checkpoint is required")
	}
	for index, write := range params.Writes {
		if write.Event.SourceID != params.SourceID {
			return CommitPollResult{}, invalid(fmt.Sprintf("event %d belongs to another source", index))
		}
		if strings.TrimSpace(string(write.Event.ID)) == "" || strings.TrimSpace(write.Outbox.ID) == "" {
			return CommitPollResult{}, invalid(fmt.Sprintf("event %d requires stable event and outbox IDs", index))
		}
		if err := validateCanonicalEvent(write.Event); err != nil {
			return CommitPollResult{}, fmt.Errorf("event %d: %w", index, err)
		}
		if err := validateOutboxMessage(write.Outbox); err != nil {
			return CommitPollResult{}, fmt.Errorf("event %d: %w", index, err)
		}
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CommitPollResult{}, fmt.Errorf("postgres store: commit poll: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lockQuery := lockSourceLeaseSQL
	lockArguments := []any{params.SourceID, params.LeaseToken}
	if params.ActiveRegion != "" || params.OwnershipEpoch != 0 {
		if !validRegion(params.ActiveRegion) || params.OwnershipEpoch <= 0 {
			return CommitPollResult{}, invalid("regional commit requires a valid region and ownership epoch")
		}
		lockQuery = lockRegionalSourceLeaseSQL
		lockArguments = append(lockArguments, params.ActiveRegion, params.OwnershipEpoch)
	}
	var source string
	if err := tx.QueryRow(ctx, lockQuery, lockArguments...).Scan(&source); err != nil {
		if isNoRows(err) {
			return CommitPollResult{}, fmt.Errorf("%w: commit source %s", ErrLeaseLost, params.SourceID)
		}
		return CommitPollResult{}, fmt.Errorf("postgres store: lock source lease: %w", err)
	}

	result := CommitPollResult{}
	for index, write := range params.Writes {
		inserted, err := insertEventWrite(ctx, tx, write)
		if err != nil {
			return CommitPollResult{}, fmt.Errorf("postgres store: commit poll event %d: %w", index, err)
		}
		if inserted {
			result.InsertedEvents++
		}
	}

	if params.State != nil {
		if params.State.SourceID != params.SourceID {
			return CommitPollResult{}, invalid("projection belongs to another source")
		}
		if err := projectReconciledState(ctx, tx, *params.State); err != nil {
			return CommitPollResult{}, err
		}
	}

	var successfulAt any
	if !params.SuccessfulAt.IsZero() {
		successfulAt = params.SuccessfulAt
	}
	checkpoint := params.Checkpoint
	completeQuery := completePollSQL
	completeArguments := []any{params.SourceID, params.LeaseToken, params.NextPollAt, successfulAt, &checkpoint, params.HealthState}
	if params.ActiveRegion != "" {
		completeQuery = completeRegionalPollSQL
		completeArguments = append(completeArguments, params.ActiveRegion, params.OwnershipEpoch)
	}
	tag, err := tx.Exec(ctx, completeQuery, completeArguments...)
	if err != nil {
		return CommitPollResult{}, fmt.Errorf("postgres store: commit poll source: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return CommitPollResult{}, fmt.Errorf("%w: commit source %s", ErrLeaseLost, params.SourceID)
	}
	if err := tx.Commit(ctx); err != nil {
		return CommitPollResult{}, fmt.Errorf("postgres store: commit poll transaction: %w", err)
	}
	return result, nil
}

func insertEventWrite(ctx context.Context, tx pgx.Tx, write EventWrite) (bool, error) {
	event := write.Event
	var eventID string
	entityID := nullableString(event.EntityID)
	if err := tx.QueryRow(
		ctx,
		insertCanonicalEventSQL,
		string(event.ID),
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
			return false, nil
		}
		return false, fmt.Errorf("insert canonical event: %w", err)
	}

	var availableAt any
	if !write.Outbox.AvailableAt.IsZero() {
		availableAt = write.Outbox.AvailableAt
	}
	if _, err := tx.Exec(
		ctx,
		insertOutboxSQL,
		write.Outbox.ID,
		eventID,
		write.Outbox.Subject,
		write.Outbox.Payload,
		availableAt,
	); err != nil {
		return false, fmt.Errorf("insert outbox: %w", err)
	}
	return true, nil
}
