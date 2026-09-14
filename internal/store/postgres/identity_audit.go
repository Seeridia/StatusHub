package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/audit"
	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AuditActor struct {
	Type      string
	ID        string
	RequestID string
}

type CreateServiceAccountParams struct {
	ID       string
	TenantID string
	Name     string
	Role     auth.Role
	Actor    AuditActor
}

type ServiceAccount struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Name       string     `json:"name"`
	Role       auth.Role  `json:"role"`
	Enabled    bool       `json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type ServiceAccountCredential struct {
	ServiceAccount ServiceAccount `json:"service_account"`
	Token          string         `json:"token"`
}

func (s *Store) CreateServiceAccount(ctx context.Context, params CreateServiceAccountParams) (ServiceAccountCredential, error) {
	if err := s.ready(); err != nil {
		return ServiceAccountCredential{}, err
	}
	if params.ID == "" {
		params.ID = uuid.NewString()
	}
	params.Name = strings.TrimSpace(params.Name)
	if _, err := uuid.Parse(params.ID); err != nil {
		return ServiceAccountCredential{}, invalid("service account ID must be a UUID")
	}
	if _, err := uuid.Parse(params.TenantID); err != nil || params.Name == "" || len(params.Name) > 128 || !params.Role.Valid() {
		return ServiceAccountCredential{}, invalid("service account tenant, name, or role is invalid")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("postgres store: generate service account token: %w", err)
	}
	token := "sa." + params.ID + "." + base64.RawURLEncoding.EncodeToString(secret)
	tokenHash := sha256.Sum256([]byte(token))
	account := ServiceAccount{ID: params.ID, TenantID: params.TenantID, Name: params.Name, Role: params.Role, Enabled: true}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("postgres store: create service account: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO service_accounts(id,tenant_id,name,role,token_hash)
VALUES($1,$2,$3,$4,$5)`, account.ID, account.TenantID, account.Name, string(account.Role), tokenHash[:]); err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("postgres store: create service account: %w", err)
	}
	metadata, _ := json.Marshal(map[string]any{"name": account.Name, "role": account.Role})
	if _, err := appendAuditTx(ctx, tx, account.TenantID, auditInput(params.Actor, "identity.service_account.create", "service_account", account.ID, metadata)); err != nil {
		return ServiceAccountCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("postgres store: create service account: commit: %w", err)
	}
	return ServiceAccountCredential{ServiceAccount: account, Token: token}, nil
}

func (s *Store) AuthenticateServiceAccount(ctx context.Context, tenantID, token string) (auth.Identity, error) {
	if err := s.ready(); err != nil {
		return auth.Identity{}, err
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "sa" || len(token) > 256 {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	accountID := parts[1]
	if _, err := uuid.Parse(tenantID); err != nil {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: authenticate service account: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var identity auth.Identity
	var expectedHash []byte
	var enabled bool
	err = tx.QueryRow(ctx, `
SELECT tenant_id,id,role,enabled,token_hash FROM service_accounts
WHERE id=$1 AND tenant_id=$2`, accountID, tenantID).Scan(
		&identity.TenantID, &identity.ActorID, &identity.Role, &enabled, &expectedHash)
	actualHash := sha256.Sum256([]byte(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = subtle.ConstantTimeCompare(make([]byte, sha256.Size), actualHash[:])
			return auth.Identity{}, auth.ErrUnauthenticated
		}
		return auth.Identity{}, fmt.Errorf("postgres store: read service account: %w", err)
	}
	if !enabled || len(expectedHash) != sha256.Size || subtle.ConstantTimeCompare(expectedHash, actualHash[:]) != 1 || !identity.Role.Valid() {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	identity.ActorType = "service_account"
	if _, err := tx.Exec(ctx, `UPDATE service_accounts SET last_used_at=statement_timestamp()
WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<statement_timestamp()-interval '5 minutes')`, accountID); err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: update service account use: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: authenticate service account: commit: %w", err)
	}
	return identity, nil
}

func (s *Store) AppendAuditEvent(ctx context.Context, tenantID string, input audit.AppendInput) (audit.Event, error) {
	if err := s.ready(); err != nil {
		return audit.Event{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return audit.Event{}, invalid("audit tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return audit.Event{}, fmt.Errorf("postgres store: append audit event: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	event, err := appendAuditTx(ctx, tx, tenantID, input)
	if err != nil {
		return audit.Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return audit.Event{}, fmt.Errorf("postgres store: append audit event: commit: %w", err)
	}
	return event, nil
}

func appendAuditTx(ctx context.Context, tx pgx.Tx, tenantID string, input audit.AppendInput) (audit.Event, error) {
	if input.OccurredAt.IsZero() {
		input.OccurredAt = time.Now().UTC()
	}
	if strings.TrimSpace(input.ActorType) == "" || strings.TrimSpace(input.ActorID) == "" || strings.TrimSpace(input.Action) == "" ||
		strings.TrimSpace(input.ResourceType) == "" || strings.TrimSpace(input.ResourceID) == "" || strings.TrimSpace(input.Outcome) == "" {
		return audit.Event{}, invalid("audit actor, action, resource, and outcome are required")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_heads(tenant_id) VALUES($1) ON CONFLICT DO NOTHING`, tenantID); err != nil {
		return audit.Event{}, fmt.Errorf("postgres store: initialize audit head: %w", err)
	}
	var sequence int64
	var previous []byte
	if err := tx.QueryRow(ctx, `SELECT last_sequence,last_hash FROM audit_heads WHERE tenant_id=$1 FOR UPDATE`, tenantID).Scan(&sequence, &previous); err != nil {
		return audit.Event{}, fmt.Errorf("postgres store: lock audit head: %w", err)
	}
	event, eventHash, err := audit.NewEvent(tenantID, sequence+1, previous, input)
	if err != nil {
		return audit.Event{}, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit_events(tenant_id,sequence,occurred_at,actor_type,actor_id,action,resource_type,resource_id,
    outcome,request_id,metadata,previous_hash,event_hash)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13)`,
		event.TenantID, event.Sequence, event.OccurredAt, event.ActorType, event.ActorID, event.Action,
		event.ResourceType, event.ResourceID, event.Outcome, event.RequestID, event.Metadata, previous, eventHash); err != nil {
		return audit.Event{}, fmt.Errorf("postgres store: insert audit event: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE audit_heads SET last_sequence=$2,last_hash=$3,updated_at=statement_timestamp() WHERE tenant_id=$1`, tenantID, event.Sequence, eventHash); err != nil {
		return audit.Event{}, fmt.Errorf("postgres store: advance audit head: %w", err)
	}
	return event, nil
}

func (s *Store) ExportAuditEvents(ctx context.Context, tenantID string, afterSequence int64, limit int) ([]audit.Event, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(tenantID); err != nil || afterSequence < 0 || limit <= 0 || limit > 10000 {
		return nil, invalid("audit export tenant, cursor, or limit is invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: export audit events: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT tenant_id,sequence,occurred_at,actor_type,actor_id,action,resource_type,resource_id,
       outcome,COALESCE(request_id,''),metadata,previous_hash,event_hash
FROM audit_events WHERE tenant_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, tenantID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: export audit events: %w", err)
	}
	defer rows.Close()
	events := make([]audit.Event, 0, limit)
	for rows.Next() {
		var event audit.Event
		var previous, eventHash []byte
		if err := rows.Scan(&event.TenantID, &event.Sequence, &event.OccurredAt, &event.ActorType, &event.ActorID,
			&event.Action, &event.ResourceType, &event.ResourceID, &event.Outcome, &event.RequestID,
			&event.Metadata, &previous, &eventHash); err != nil {
			return nil, fmt.Errorf("postgres store: scan audit event: %w", err)
		}
		event.PreviousHash, event.EventHash = hex.EncodeToString(previous), hex.EncodeToString(eventHash)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan audit events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: export audit events: commit: %w", err)
	}
	return events, nil
}

func auditInput(actor AuditActor, action, resourceType, resourceID string, metadata json.RawMessage) audit.AppendInput {
	if strings.TrimSpace(actor.Type) == "" {
		actor.Type = "system"
	}
	if strings.TrimSpace(actor.ID) == "" {
		actor.ID = "statushub-admin"
	}
	return audit.AppendInput{OccurredAt: time.Now().UTC(), ActorType: actor.Type, ActorID: actor.ID,
		Action: action, ResourceType: resourceType, ResourceID: resourceID, Outcome: "success",
		RequestID: actor.RequestID, Metadata: metadata}
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}
