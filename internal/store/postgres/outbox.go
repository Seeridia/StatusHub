package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const claimOutboxSQL = `
WITH due AS (
    SELECT id
    FROM outbox
    WHERE published_at IS NULL
      AND available_at <= statement_timestamp()
      AND (lease_until IS NULL OR lease_until <= statement_timestamp())
    ORDER BY available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), leased AS (
    UPDATE outbox AS message
    SET lease_owner = $2,
        lease_token = gen_random_uuid(),
        lease_until = statement_timestamp() + ($3::double precision * interval '1 microsecond'),
        attempts = message.attempts + 1
    FROM due
    WHERE message.id = due.id
    RETURNING message.id,
              message.event_id,
              message.subject,
              message.payload,
              message.available_at,
              message.attempts,
              message.last_error,
              message.created_at,
              message.lease_owner,
              message.lease_token,
              message.lease_until
)
SELECT id,
       event_id,
       subject,
       payload,
       available_at,
       attempts,
       last_error,
       created_at,
       lease_owner,
       lease_token,
       lease_until
FROM leased
ORDER BY available_at, id`

const markOutboxPublishedSQL = `
UPDATE outbox
SET published_at = COALESCE($3, statement_timestamp()),
    lease_owner = NULL,
    lease_token = NULL,
    lease_until = NULL,
    last_error = NULL
WHERE id = $1
  AND lease_token = $2
  AND lease_until > statement_timestamp()
  AND published_at IS NULL`

const failOutboxSQL = `
UPDATE outbox
SET available_at = $3,
    lease_owner = NULL,
    lease_token = NULL,
    lease_until = NULL,
    last_error = $4
WHERE id = $1
  AND lease_token = $2
  AND lease_until > statement_timestamp()
  AND published_at IS NULL`

// ClaimOutbox claims unpublished messages in a short transaction. Publishing
// must happen after this method returns, outside the transaction.
func (s *Store) ClaimOutbox(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]OutboxLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := required(owner, "lease owner"); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, invalid("limit must be greater than zero")
	}
	if leaseDuration < time.Microsecond {
		return nil, invalid("lease duration must be at least one microsecond")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim outbox: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, claimOutboxSQL, limit, owner, leaseDuration.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim outbox: query: %w", err)
	}
	defer rows.Close()

	messages := make([]OutboxLease, 0, limit)
	for rows.Next() {
		var message OutboxLease
		if err := rows.Scan(
			&message.ID,
			&message.EventID,
			&message.Subject,
			&message.Payload,
			&message.AvailableAt,
			&message.Attempts,
			&message.LastError,
			&message.CreatedAt,
			&message.LeaseOwner,
			&message.LeaseToken,
			&message.LeaseUntil,
		); err != nil {
			return nil, fmt.Errorf("postgres store: claim outbox: scan: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: claim outbox: rows: %w", err)
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: claim outbox: commit: %w", err)
	}
	return messages, nil
}

// MarkOutboxPublished commits publication only for the current lease token.
// A zero publishedAt uses PostgreSQL's transaction timestamp.
func (s *Store) MarkOutboxPublished(
	ctx context.Context,
	id string,
	leaseToken string,
	publishedAt time.Time,
) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateOutboxCAS(id, leaseToken); err != nil {
		return err
	}

	var timestamp *time.Time
	if !publishedAt.IsZero() {
		timestamp = &publishedAt
	}
	tag, err := s.db.Exec(ctx, markOutboxPublishedSQL, id, leaseToken, timestamp)
	if err != nil {
		return fmt.Errorf("postgres store: mark outbox published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: publish outbox %s", ErrLeaseLost, id)
	}
	return nil
}

// FailOutbox releases a failed publication attempt and schedules its retry.
// The update is fenced by the same lease token as successful publication.
func (s *Store) FailOutbox(ctx context.Context, params FailOutboxParams) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validateOutboxCAS(params.ID, params.LeaseToken); err != nil {
		return err
	}
	if params.AvailableAt.IsZero() {
		return invalid("outbox available time is required")
	}

	tag, err := s.db.Exec(
		ctx,
		failOutboxSQL,
		params.ID,
		params.LeaseToken,
		params.AvailableAt,
		params.LastError,
	)
	if err != nil {
		return fmt.Errorf("postgres store: fail outbox: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: fail outbox %s", ErrLeaseLost, params.ID)
	}
	return nil
}

func validateOutboxCAS(id, leaseToken string) error {
	if err := required(id, "outbox ID"); err != nil {
		return err
	}
	return required(leaseToken, "lease token")
}
