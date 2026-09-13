package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ClaimDeliveryLane uses a per-tenant lateral limit. Critical first attempts,
// retries, and bulk first attempts are disjoint so provider throttling or a
// large low-priority burst cannot consume another lane's worker capacity.
func (s *Store) ClaimDeliveryLane(ctx context.Context, owner string, lane DeliveryLane, limit, perTenant int, leaseDuration time.Duration) ([]DeliveryLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(owner) == "" || !lane.Valid() || limit <= 0 || perTenant <= 0 || leaseDuration <= 0 {
		return nil, invalid("delivery claim arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim deliveries: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH tenant_candidates AS (
    SELECT ep.tenant_id, min(d.next_attempt_at) AS oldest
    FROM deliveries d JOIN endpoints ep ON ep.id=d.endpoint_id
    WHERE (
        ($5='critical' AND d.queue_class='critical' AND d.status='queued'
         AND d.next_attempt_at <= statement_timestamp()
         AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
        OR ($5='bulk' AND d.queue_class='bulk' AND d.status='queued'
         AND d.next_attempt_at <= statement_timestamp()
         AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
        OR ($5='retry' AND (
            (d.status='retry_wait' AND d.next_attempt_at <= statement_timestamp()
             AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
            OR (d.status='sending' AND d.lease_until <= statement_timestamp())
        ))
      ) AND ep.enabled AND ep.channel <> 'private_agent' AND EXISTS (SELECT 1 FROM subscriptions active_rule WHERE active_rule.id=d.subscription_id AND active_rule.deleted_at IS NULL)
    GROUP BY ep.tenant_id ORDER BY oldest LIMIT $2
), candidates AS (
    SELECT picked.id
    FROM tenant_candidates tenant
    CROSS JOIN LATERAL (
        SELECT d.id
        FROM deliveries d JOIN endpoints ep ON ep.id=d.endpoint_id
        WHERE ep.tenant_id=tenant.tenant_id AND ep.enabled AND ep.channel <> 'private_agent' AND EXISTS (SELECT 1 FROM subscriptions active_rule WHERE active_rule.id=d.subscription_id AND active_rule.deleted_at IS NULL)
          AND (
            ($5='critical' AND d.queue_class='critical' AND d.status='queued'
             AND d.next_attempt_at <= statement_timestamp()
             AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
            OR ($5='bulk' AND d.queue_class='bulk' AND d.status='queued'
             AND d.next_attempt_at <= statement_timestamp()
             AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
            OR ($5='retry' AND (
                (d.status='retry_wait' AND d.next_attempt_at <= statement_timestamp()
                 AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
                OR (d.status='sending' AND d.lease_until <= statement_timestamp())
            ))
          )
          AND NOT EXISTS (
              SELECT 1 FROM deliveries earlier
              WHERE earlier.endpoint_id=d.endpoint_id
                AND EXISTS (SELECT 1 FROM subscriptions earlier_rule WHERE earlier_rule.id=earlier.subscription_id AND earlier_rule.deleted_at IS NULL)
                AND earlier.status IN ('queued','retry_wait','sending')
                AND (earlier.created_at, earlier.id) < (d.created_at, d.id)
          )
        ORDER BY d.priority DESC, d.next_attempt_at, d.id
        FOR UPDATE OF d SKIP LOCKED
        LIMIT $3
    ) picked
    LIMIT $2
), claimed AS (
    UPDATE deliveries d
    SET status='sending', lease_owner=$1, lease_token=gen_random_uuid(),
        lease_until=statement_timestamp()+$4::interval,
        attempt_count=d.attempt_count+1,
        first_attempt_at=COALESCE(d.first_attempt_at, statement_timestamp()),
        updated_at=statement_timestamp()
    FROM candidates c WHERE d.id=c.id
    RETURNING d.*
), attempts AS (
    INSERT INTO delivery_attempts (id, delivery_id, attempt_number, secret_version, status, started_at)
    SELECT gen_random_uuid(), c.id, c.attempt_count, ep.secret_version, 'sending', statement_timestamp()
    FROM claimed c JOIN endpoints ep ON ep.id=c.endpoint_id
)
SELECT c.id, c.event_id, c.subscription_id, c.endpoint_id,
       ep.channel, ep.encrypted_config, ep.key_id, ep.secret_version,
       c.attempt_count, c.lease_token, c.lease_until, c.eligible_at,
       src.canonical_url, ce.event_kind, ce.entity_id, ce.aggregate_revision,
       ce.canonical_schema_version, ce.canonical_payload, ce.observed_at,
       $5::text
FROM claimed c
JOIN endpoints ep ON ep.id=c.endpoint_id
JOIN canonical_events ce ON ce.id=c.event_id
JOIN sources src ON src.id=ce.source_id
ORDER BY c.priority DESC, c.next_attempt_at, c.id`, owner, limit, perTenant, leaseDuration.String(), string(lane))
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim deliveries: %w", err)
	}
	defer rows.Close()
	leases := make([]DeliveryLease, 0, limit)
	for rows.Next() {
		var lease DeliveryLease
		var kind string
		var revision int64
		if err := rows.Scan(&lease.ID, &lease.EventID, &lease.SubscriptionID, &lease.EndpointID,
			&lease.Channel, &lease.EncryptedConfig, &lease.KeyID, &lease.SecretVersion,
			&lease.AttemptNumber, &lease.LeaseToken, &lease.LeaseUntil, &lease.EligibleAt,
			&lease.EventSource, &kind, &lease.EventEntityID, &revision,
			&lease.EventSchemaVersion, &lease.EventPayload, &lease.EventObservedAt, &lease.Lane); err != nil {
			return nil, fmt.Errorf("postgres store: scan delivery lease: %w", err)
		}
		lease.EventKind = domain.EventKind(kind)
		lease.EventRevision = uint64(revision)
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan delivery leases: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: claim deliveries: commit: %w", err)
	}
	return leases, nil
}

func (s *Store) CompleteDeliveryAttempt(ctx context.Context, params CompleteDeliveryParams) error {
	if err := s.ready(); err != nil {
		return err
	}
	if params.DeliveryID == "" || params.LeaseToken == "" || params.AttemptNumber <= 0 || params.FinishedAt.IsZero() {
		return invalid("delivery completion identity is invalid")
	}
	if params.AgentID != "" {
		if _, err := uuid.Parse(params.AgentID); err != nil {
			return invalid("private agent ID must be a UUID")
		}
	}
	var agentID any
	if params.AgentID != "" {
		agentID = params.AgentID
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres store: complete delivery: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var endpointID string
	err = tx.QueryRow(ctx, `
SELECT endpoint_id FROM deliveries
WHERE id=$1 AND lease_token=$2 AND lease_until > statement_timestamp()
  AND ($3::uuid IS NULL OR EXISTS (
      SELECT 1 FROM private_agent_endpoints pae
      WHERE pae.agent_id=$3::uuid AND pae.endpoint_id=deliveries.endpoint_id
  ))
FOR UPDATE`, params.DeliveryID, params.LeaseToken, agentID).Scan(&endpointID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("postgres store: lock delivery: %w", err)
	}
	_, err = tx.Exec(ctx, `
UPDATE delivery_attempts
SET status=$3, finished_at=$4, http_status=NULLIF($5,0), provider_code=NULLIF($6,''),
    provider_message_id=NULLIF($7,''), retry_after=$8, error_class=NULLIF($9,''), error_summary=NULLIF($10,'')
WHERE delivery_id=$1 AND attempt_number=$2`, params.DeliveryID, params.AttemptNumber,
		params.Status, params.FinishedAt, params.HTTPStatus, params.ProviderCode, params.ProviderMessageID,
		params.RetryAt, params.ErrorClass, params.ErrorSummary)
	if err != nil {
		return fmt.Errorf("postgres store: update delivery attempt: %w", err)
	}
	nextAttempt := any(nil)
	if params.RetryAt != nil {
		nextAttempt = *params.RetryAt
	}
	tag, err := tx.Exec(ctx, `
UPDATE deliveries SET status=$3, provider_message_id=COALESCE(NULLIF($4,''),provider_message_id),
    next_attempt_at=COALESCE($5,next_attempt_at),
    accepted_at=CASE WHEN $3='accepted' THEN $6 ELSE accepted_at END,
    last_error_class=NULLIF($7,''), last_error_summary=NULLIF($8,''),
    dead_lettered_at=CASE WHEN $3='dead_letter' THEN $6 ELSE dead_lettered_at END,
    dead_letter_reason=CASE WHEN $3='dead_letter' THEN NULLIF($9,'') ELSE dead_letter_reason END,
    lease_owner=NULL, lease_token=NULL, lease_until=NULL, updated_at=statement_timestamp()
WHERE id=$1 AND lease_token=$2`, params.DeliveryID, params.LeaseToken, params.Status,
		params.ProviderMessageID, nextAttempt, params.FinishedAt, params.ErrorClass, params.ErrorSummary, params.DeadLetterReason)
	if err != nil {
		return fmt.Errorf("postgres store: update delivery: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	if params.DisableEndpoint {
		if _, err := tx.Exec(ctx, `UPDATE endpoints SET enabled=false, health_state='disabled', updated_at=statement_timestamp() WHERE id=$1`, endpointID); err != nil {
			return fmt.Errorf("postgres store: disable endpoint: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres store: complete delivery: commit: %w", err)
	}
	return nil
}
