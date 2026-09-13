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
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const currentRuleSetVersion = 1

func (s *Store) EnsureFanoutPlan(ctx context.Context, eventID string, shardCount int) (string, bool, error) {
	if err := s.ready(); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(eventID) == "" || shardCount <= 0 || shardCount > 256 {
		return "", false, invalid("fanout event ID and shard count are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, fmt.Errorf("postgres store: ensure fanout plan: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	planID := uuid.NewString()
	created := true
	err = tx.QueryRow(ctx, `
INSERT INTO fanout_plans (id, event_id, rule_version, shard_count)
VALUES ($1, $2, $3, $4)
ON CONFLICT (event_id, rule_version) DO NOTHING
RETURNING id`, planID, eventID, currentRuleSetVersion, shardCount).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		err = tx.QueryRow(ctx, `SELECT id FROM fanout_plans WHERE event_id=$1 AND rule_version=$2`, eventID, currentRuleSetVersion).Scan(&planID)
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres store: ensure fanout plan: %w", err)
	}
	if created {
		if _, err = tx.Exec(ctx, `
INSERT INTO fanout_plan_shards (plan_id, shard_number)
SELECT $1, shard FROM generate_series(0, $2 - 1) AS shard`, planID, shardCount); err != nil {
			return "", false, fmt.Errorf("postgres store: create fanout shards: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("postgres store: ensure fanout plan: commit: %w", err)
	}
	return planID, created, nil
}

func (s *Store) ClaimFanoutShards(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]FanoutShardLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(owner) == "" || limit <= 0 || leaseDuration <= 0 {
		return nil, invalid("fanout claim arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim fanout shards: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH candidates AS (
    SELECT fps.plan_id, fps.shard_number
    FROM fanout_plan_shards fps
    JOIN fanout_plans fp ON fp.id = fps.plan_id
    WHERE fps.status <> 'completed'
      AND (fps.lease_until IS NULL OR fps.lease_until <= statement_timestamp())
    ORDER BY fp.created_at, fps.shard_number
    FOR UPDATE OF fps SKIP LOCKED
    LIMIT $2
)
UPDATE fanout_plan_shards fps
SET status='running', lease_owner=$1, lease_token=gen_random_uuid(),
    lease_until=statement_timestamp()+$3::interval, attempts=fps.attempts+1
FROM candidates c, fanout_plans fp
WHERE fps.plan_id=c.plan_id AND fps.shard_number=c.shard_number AND fp.id=fps.plan_id
RETURNING fps.plan_id, fp.event_id, fps.shard_number, fp.shard_count,
          fps.cursor_subscription_id, fps.lease_owner, fps.lease_token, fps.lease_until`, owner, limit, leaseDuration.String())
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim fanout shards: %w", err)
	}
	defer rows.Close()
	leases := make([]FanoutShardLease, 0, limit)
	for rows.Next() {
		var lease FanoutShardLease
		if err := rows.Scan(&lease.PlanID, &lease.EventID, &lease.ShardNumber, &lease.ShardCount,
			&lease.CursorSubscriptionID, &lease.LeaseOwner, &lease.LeaseToken, &lease.LeaseUntil); err != nil {
			return nil, fmt.Errorf("postgres store: scan fanout shard: %w", err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan fanout shards: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: claim fanout shards: commit: %w", err)
	}
	return leases, nil
}

func (s *Store) LoadFanoutEvent(ctx context.Context, eventID string) (FanoutEvent, error) {
	if err := s.ready(); err != nil {
		return FanoutEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return FanoutEvent{}, fmt.Errorf("postgres store: load fanout event: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var event FanoutEvent
	var kind string
	err = tx.QueryRow(ctx, `
SELECT id, event_kind, canonical_payload, observed_at, ingested_at
FROM canonical_events WHERE id=$1`, eventID).Scan(&event.ID, &kind, &event.Payload, &event.ObservedAt, &event.IngestedAt)
	if err != nil {
		return FanoutEvent{}, fmt.Errorf("postgres store: load fanout event: %w", err)
	}
	event.Kind = domain.EventKind(kind)
	if err := tx.Commit(ctx); err != nil {
		return FanoutEvent{}, fmt.Errorf("postgres store: load fanout event: commit: %w", err)
	}
	return event, nil
}

func (s *Store) LoadFanoutCandidates(ctx context.Context, lease FanoutShardLease, limit int) ([]FanoutCandidate, *string, bool, error) {
	if err := s.ready(); err != nil {
		return nil, nil, false, err
	}
	if limit <= 0 || lease.ShardCount <= 0 || lease.ShardNumber < 0 || lease.ShardNumber >= lease.ShardCount {
		return nil, nil, false, invalid("fanout candidate arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, false, fmt.Errorf("postgres store: load fanout candidates: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH event AS (
    SELECT ce.id, ce.source_id, ce.event_kind, ce.entity_id, ce.canonical_payload,
           src.vendor_id, src.tenant_id AS source_tenant_id
    FROM canonical_events ce JOIN sources src ON src.id=ce.source_id
    WHERE ce.id=$1
), candidate_subscriptions AS (
    SELECT s.id, s.tenant_id, s.rule_version, s.rule
    FROM subscriptions s CROSS JOIN event e
    WHERE s.enabled
	  AND (e.source_tenant_id IS NULL OR s.tenant_id=e.source_tenant_id)
	  AND EXISTS (
	      SELECT 1 FROM subscription_endpoints active_se
	      JOIN endpoints active_ep ON active_ep.id=active_se.endpoint_id AND active_ep.enabled
	      WHERE active_se.subscription_id=s.id
	  )
      AND get_byte(uuid_send(s.id), 0) % $2 = $3
      AND ($4::uuid IS NULL OR s.id > $4)
      AND (
        NOT EXISTS (SELECT 1 FROM subscription_scopes none WHERE none.subscription_id=s.id)
        OR EXISTS (
          SELECT 1 FROM subscription_scopes ss
          WHERE ss.subscription_id=s.id
            AND (ss.vendor_id IS NULL OR ss.vendor_id=e.vendor_id)
            AND (ss.event_kind IS NULL OR ss.event_kind=e.event_kind)
            AND (
              ss.component_key IS NULL
              OR ss.component_key=e.entity_id
              OR COALESCE(e.canonical_payload->'current'->'component_ids', '[]'::jsonb) ? ss.component_key
            )
            AND (
              ss.tag IS NULL
              OR COALESCE(e.canonical_payload->'current'->'tags', '{}'::jsonb) ? ss.tag
            )
        )
      )
    ORDER BY s.id
    LIMIT $5
)
SELECT cs.id, se.endpoint_id, cs.rule_version, cs.rule
FROM candidate_subscriptions cs
JOIN subscription_endpoints se ON se.subscription_id=cs.id
JOIN endpoints ep ON ep.id=se.endpoint_id AND ep.enabled AND ep.tenant_id=cs.tenant_id
ORDER BY cs.id, se.endpoint_id`, lease.EventID, lease.ShardCount, lease.ShardNumber, lease.CursorSubscriptionID, limit+1)
	if err != nil {
		return nil, nil, false, fmt.Errorf("postgres store: load fanout candidates: %w", err)
	}
	defer rows.Close()
	grouped := make([]FanoutCandidate, 0, limit)
	subscriptions := make([]string, 0, limit+1)
	seen := make(map[string]struct{})
	for rows.Next() {
		var candidate FanoutCandidate
		if err := rows.Scan(&candidate.SubscriptionID, &candidate.EndpointID, &candidate.RuleVersion, &candidate.Rule); err != nil {
			return nil, nil, false, fmt.Errorf("postgres store: scan fanout candidate: %w", err)
		}
		if _, ok := seen[candidate.SubscriptionID]; !ok {
			seen[candidate.SubscriptionID] = struct{}{}
			subscriptions = append(subscriptions, candidate.SubscriptionID)
		}
		grouped = append(grouped, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, fmt.Errorf("postgres store: scan fanout candidates: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, false, fmt.Errorf("postgres store: load fanout candidates: commit: %w", err)
	}
	more := len(subscriptions) > limit
	if more {
		cutoff := subscriptions[limit-1]
		kept := grouped[:0]
		for _, candidate := range grouped {
			if candidate.SubscriptionID <= cutoff {
				kept = append(kept, candidate)
			}
		}
		grouped = kept
		subscriptions = subscriptions[:limit]
	}
	var cursor *string
	if len(subscriptions) > 0 {
		value := subscriptions[len(subscriptions)-1]
		cursor = &value
	}
	return grouped, cursor, !more, nil
}

func (s *Store) CommitFanoutShard(ctx context.Context, params CommitFanoutShardParams) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(params.PlanID) == "" || strings.TrimSpace(params.LeaseToken) == "" || params.ShardNumber < 0 {
		return 0, invalid("fanout commit arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("postgres store: commit fanout shard: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var eventID string
	err = tx.QueryRow(ctx, `
SELECT fp.event_id FROM fanout_plan_shards fps
JOIN fanout_plans fp ON fp.id=fps.plan_id
WHERE fps.plan_id=$1 AND fps.shard_number=$2 AND fps.lease_token=$3
  AND fps.lease_until > statement_timestamp()
FOR UPDATE OF fps`, params.PlanID, params.ShardNumber, params.LeaseToken).Scan(&eventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrLeaseLost
	}
	if err != nil {
		return 0, fmt.Errorf("postgres store: lock fanout shard: %w", err)
	}
	inserted := int64(0)
	if len(params.Deliveries) > 0 {
		for _, delivery := range params.Deliveries {
			if _, parseErr := uuid.Parse(delivery.ID); parseErr != nil {
				return 0, invalid("fanout delivery ID is not a UUID")
			}
			if _, parseErr := uuid.Parse(delivery.SubscriptionID); parseErr != nil {
				return 0, invalid("fanout subscription ID is not a UUID")
			}
			if _, parseErr := uuid.Parse(delivery.EndpointID); parseErr != nil {
				return 0, invalid("fanout endpoint ID is not a UUID")
			}
		}
		payload, marshalErr := json.Marshal(params.Deliveries)
		if marshalErr != nil {
			return 0, fmt.Errorf("postgres store: encode fanout deliveries: %w", marshalErr)
		}
		tag, insertErr := tx.Exec(ctx, `
INSERT INTO deliveries (
    id, event_id, subscription_id, endpoint_id, template_version,
    matched_rule_version, status, priority, eligible_at, next_attempt_at
)
SELECT input.id, $1, input.subscription_id, input.endpoint_id,
       input.template_version, input.rule_version, 'queued', input.priority,
       input.eligible_at, input.eligible_at
FROM jsonb_to_recordset($2::jsonb) AS input(
    id uuid, subscription_id uuid, endpoint_id uuid, template_version integer,
    rule_version integer, priority smallint, eligible_at timestamptz
)
ON CONFLICT (event_id, subscription_id, endpoint_id, template_version) DO NOTHING`,
			eventID, string(payload))
		if insertErr != nil {
			return 0, fmt.Errorf("postgres store: insert fanout deliveries: %w", insertErr)
		}
		inserted = tag.RowsAffected()
	}
	status := "queued"
	var completedAt any
	if params.Completed {
		status = "completed"
		completedAt = time.Now().UTC()
	}
	tag, err := tx.Exec(ctx, `
UPDATE fanout_plan_shards
SET cursor_subscription_id=COALESCE($4, cursor_subscription_id), status=$5,
    matched_deliveries=matched_deliveries+$6,
    completed_at=COALESCE($7, completed_at), lease_owner=NULL, lease_token=NULL, lease_until=NULL
WHERE plan_id=$1 AND shard_number=$2 AND lease_token=$3`, params.PlanID, params.ShardNumber,
		params.LeaseToken, params.CursorSubscriptionID, status, inserted, completedAt)
	if err != nil {
		return 0, fmt.Errorf("postgres store: update fanout shard: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, ErrLeaseLost
	}
	if params.Completed {
		if _, err := tx.Exec(ctx, `
UPDATE fanout_plans fp
SET completed_shards=(SELECT count(*) FROM fanout_plan_shards WHERE plan_id=fp.id AND status='completed'),
    status=CASE WHEN NOT EXISTS (
        SELECT 1 FROM fanout_plan_shards WHERE plan_id=fp.id AND status<>'completed'
    ) THEN 'completed' ELSE 'running' END,
    completed_at=CASE WHEN NOT EXISTS (
        SELECT 1 FROM fanout_plan_shards WHERE plan_id=fp.id AND status<>'completed'
    ) THEN statement_timestamp() ELSE NULL END
WHERE fp.id=$1`, params.PlanID); err != nil {
			return 0, fmt.Errorf("postgres store: complete fanout plan: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres store: commit fanout shard: commit: %w", err)
	}
	return inserted, nil
}
