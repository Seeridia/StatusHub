package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreatePrivateAgent(ctx context.Context, tenantID, name string) (PrivateAgentCredential, error) {
	if err := s.ready(); err != nil {
		return PrivateAgentCredential{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil || strings.TrimSpace(name) == "" || len(name) > 128 {
		return PrivateAgentCredential{}, invalid("private agent tenant UUID and name are required")
	}
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return PrivateAgentCredential{}, fmt.Errorf("postgres store: generate private agent token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	hash := sha256.Sum256([]byte(token))
	agent := PrivateAgent{ID: uuid.NewString(), TenantID: tenantID, Name: strings.TrimSpace(name), Enabled: true}
	if _, err := s.db.Exec(ctx, `
INSERT INTO private_agents(id,tenant_id,name,token_hash)
VALUES($1,$2,$3,$4)`, agent.ID, agent.TenantID, agent.Name, hash[:]); err != nil {
		return PrivateAgentCredential{}, fmt.Errorf("postgres store: create private agent: %w", err)
	}
	return PrivateAgentCredential{Agent: agent, Token: token}, nil
}

func (s *Store) BindPrivateAgentEndpoint(ctx context.Context, agentID, endpointID string) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if _, err := uuid.Parse(agentID); err != nil {
		return false, invalid("private agent ID must be a UUID")
	}
	if _, err := uuid.Parse(endpointID); err != nil {
		return false, invalid("private endpoint ID must be a UUID")
	}
	tag, err := s.db.Exec(ctx, `
INSERT INTO private_agent_endpoints(agent_id,endpoint_id,tenant_id)
SELECT pa.id,ep.id,pa.tenant_id
FROM private_agents pa
JOIN endpoints ep ON ep.id=$2 AND ep.tenant_id=pa.tenant_id
WHERE pa.id=$1 AND pa.enabled AND ep.enabled AND ep.channel='private_agent'
ON CONFLICT DO NOTHING`, agentID, endpointID)
	if err != nil {
		return false, fmt.Errorf("postgres store: bind private agent endpoint: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) AuthenticatePrivateAgent(ctx context.Context, agentID, token, version string) (PrivateAgent, error) {
	if err := s.ready(); err != nil {
		return PrivateAgent{}, err
	}
	if _, err := uuid.Parse(agentID); err != nil || len(token) < 32 || len(token) > 256 || len(version) > 128 {
		return PrivateAgent{}, invalid("private agent credentials are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PrivateAgent{}, fmt.Errorf("postgres store: authenticate private agent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var agent PrivateAgent
	var expectedHash []byte
	err = tx.QueryRow(ctx, `
SELECT id,tenant_id,name,enabled,last_seen_at,COALESCE(agent_version,''),token_hash
FROM private_agents WHERE id=$1 FOR UPDATE`, agentID).Scan(
		&agent.ID, &agent.TenantID, &agent.Name, &agent.Enabled, &agent.LastSeenAt, &agent.Version, &expectedHash,
	)
	if err != nil || !agent.Enabled {
		return PrivateAgent{}, errors.New("postgres store: private agent authentication failed")
	}
	actualHash := sha256.Sum256([]byte(token))
	if len(expectedHash) != len(actualHash) || subtle.ConstantTimeCompare(expectedHash, actualHash[:]) != 1 {
		return PrivateAgent{}, errors.New("postgres store: private agent authentication failed")
	}
	if _, err := tx.Exec(ctx, `
UPDATE private_agents SET last_seen_at=statement_timestamp(),agent_version=NULLIF($2,''),updated_at=statement_timestamp()
WHERE id=$1`, agentID, strings.TrimSpace(version)); err != nil {
		return PrivateAgent{}, fmt.Errorf("postgres store: update private agent heartbeat: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PrivateAgent{}, fmt.Errorf("postgres store: authenticate private agent: commit: %w", err)
	}
	now := time.Now().UTC()
	agent.LastSeenAt = &now
	agent.Version = strings.TrimSpace(version)
	return agent, nil
}

func (s *Store) ClaimPrivateAgentDeliveries(ctx context.Context, agentID, owner string, limit int, leaseDuration time.Duration) ([]DeliveryLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(agentID); err != nil || strings.TrimSpace(owner) == "" || limit <= 0 || limit > 100 || leaseDuration <= 0 {
		return nil, invalid("private agent claim arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim private deliveries: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH candidates AS (
    SELECT d.id,
           CASE WHEN d.status IN ('retry_wait','sending') THEN 'retry' ELSE d.queue_class END AS lane
    FROM deliveries d
    JOIN private_agent_endpoints pae ON pae.endpoint_id=d.endpoint_id AND pae.agent_id=$1
    JOIN private_agents pa ON pa.id=pae.agent_id AND pa.enabled
    JOIN endpoints ep ON ep.id=d.endpoint_id AND ep.enabled AND ep.channel='private_agent'
    JOIN subscriptions active_rule ON active_rule.id=d.subscription_id AND active_rule.deleted_at IS NULL
    WHERE (
        (d.status IN ('queued','retry_wait') AND d.next_attempt_at <= statement_timestamp()
         AND (d.lease_until IS NULL OR d.lease_until <= statement_timestamp()))
        OR (d.status='sending' AND d.lease_until <= statement_timestamp())
    )
      AND NOT EXISTS (
          SELECT 1 FROM deliveries earlier
          WHERE earlier.endpoint_id=d.endpoint_id
                AND EXISTS (SELECT 1 FROM subscriptions earlier_rule WHERE earlier_rule.id=earlier.subscription_id AND earlier_rule.deleted_at IS NULL)
            AND earlier.status IN ('queued','retry_wait','sending')
            AND (earlier.created_at,earlier.id) < (d.created_at,d.id)
      )
    ORDER BY d.priority DESC,d.next_attempt_at,d.id
    FOR UPDATE OF d SKIP LOCKED
    LIMIT $3
), claimed AS (
    UPDATE deliveries d
    SET status='sending',lease_owner=$2,lease_token=gen_random_uuid(),
        lease_until=statement_timestamp()+$4::interval,attempt_count=d.attempt_count+1,
        first_attempt_at=COALESCE(d.first_attempt_at,statement_timestamp()),updated_at=statement_timestamp()
    FROM candidates c WHERE d.id=c.id RETURNING d.*
), attempts AS (
    INSERT INTO delivery_attempts(id,delivery_id,attempt_number,secret_version,status,started_at)
    SELECT gen_random_uuid(),c.id,c.attempt_count,ep.secret_version,'sending',statement_timestamp()
    FROM claimed c JOIN endpoints ep ON ep.id=c.endpoint_id
)
SELECT c.id,c.event_id,c.subscription_id,c.endpoint_id,
       ep.channel,ep.encrypted_config,ep.key_id,ep.secret_version,
       c.attempt_count,c.lease_token,c.lease_until,c.eligible_at,
       src.canonical_url,ce.event_kind,ce.entity_id,ce.aggregate_revision,
       ce.canonical_schema_version,ce.canonical_payload,ce.observed_at,candidate.lane
FROM claimed c
JOIN candidates candidate ON candidate.id=c.id
JOIN endpoints ep ON ep.id=c.endpoint_id
JOIN canonical_events ce ON ce.id=c.event_id
JOIN sources src ON src.id=ce.source_id
ORDER BY c.priority DESC,c.next_attempt_at,c.id`, agentID, owner, limit, leaseDuration.String())
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim private deliveries: %w", err)
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
			return nil, fmt.Errorf("postgres store: scan private delivery lease: %w", err)
		}
		lease.EventKind = domain.EventKind(kind)
		lease.EventRevision = uint64(revision)
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan private delivery leases: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: claim private deliveries: commit: %w", err)
	}
	return leases, nil
}
