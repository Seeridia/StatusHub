package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/jackc/pgx/v5"
)

var ErrReplayNotAllowed = errors.New("postgres store: dead-letter replay is not allowed")

func (s *Store) ListDeadLetters(ctx context.Context, cursor *time.Time, limit int) ([]DeadLetter, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		return nil, invalid("dead-letter limit must be in [1,500]")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list dead letters: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT d.id,d.event_id,d.subscription_id,d.endpoint_id,ep.channel,ce.event_kind,
       COALESCE(ce.canonical_payload->'current'->>'name',ce.event_kind),
       d.attempt_count,d.replay_count,COALESCE(d.dead_letter_reason,''),d.dead_lettered_at,
       ep.enabled,s.enabled
FROM deliveries d
JOIN endpoints ep ON ep.id=d.endpoint_id
JOIN subscriptions s ON s.id=d.subscription_id
JOIN canonical_events ce ON ce.id=d.event_id
WHERE d.status='dead_letter' AND ($1::timestamptz IS NULL OR d.dead_lettered_at < $1)
ORDER BY d.dead_lettered_at DESC,d.id DESC LIMIT $2`, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list dead letters: %w", err)
	}
	defer rows.Close()
	result := make([]DeadLetter, 0, limit)
	for rows.Next() {
		var item DeadLetter
		var kind string
		if err := rows.Scan(&item.ID, &item.EventID, &item.SubscriptionID, &item.EndpointID, &item.Channel, &kind,
			&item.Subject, &item.AttemptCount, &item.ReplayCount, &item.Reason, &item.DeadLetteredAt,
			&item.EndpointEnabled, &item.SubscriptionEnabled); err != nil {
			return nil, fmt.Errorf("postgres store: scan dead letter: %w", err)
		}
		item.EventKind = domain.EventKind(kind)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan dead letters: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list dead letters: commit: %w", err)
	}
	return result, nil
}

func (s *Store) ReplayDeadLetter(ctx context.Context, deliveryID string, at time.Time) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if strings.TrimSpace(deliveryID) == "" {
		return false, invalid("delivery ID is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("postgres store: replay dead letter: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var allowed bool
	err = tx.QueryRow(ctx, `
SELECT d.status='dead_letter' AND s.enabled AND ep.enabled
       AND d.superseded_at IS NULL AND (d.expires_at IS NULL OR d.expires_at>$2)
       AND EXISTS (
         SELECT 1 FROM delivery_attempts da
         WHERE da.delivery_id=d.id AND da.secret_version=ep.secret_version
       )
FROM deliveries d
JOIN subscriptions s ON s.id=d.subscription_id
JOIN endpoints ep ON ep.id=d.endpoint_id
WHERE d.id=$1 FOR UPDATE OF d`, deliveryID, at).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrReplayNotAllowed
	}
	if err != nil {
		return false, fmt.Errorf("postgres store: inspect dead letter: %w", err)
	}
	if !allowed {
		return false, ErrReplayNotAllowed
	}
	tag, err := tx.Exec(ctx, `
UPDATE deliveries SET status='queued',next_attempt_at=$2,replay_count=replay_count+1,
    dead_lettered_at=NULL,dead_letter_reason=NULL,last_error_class=NULL,last_error_summary=NULL,
    lease_owner=NULL,lease_token=NULL,lease_until=NULL,updated_at=statement_timestamp()
WHERE id=$1 AND status='dead_letter'`, deliveryID, at)
	if err != nil {
		return false, fmt.Errorf("postgres store: replay dead letter: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, ErrReplayNotAllowed
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres store: replay dead letter: commit: %w", err)
	}
	return true, nil
}
