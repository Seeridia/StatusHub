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

type UpsertOIDCProviderParams struct {
	ID             string
	TenantID       string
	Issuer         string
	ClientID       string
	JWKSURI        string
	AllowedDomains []string
	Enabled        bool
	Actor          AuditActor
}

type SetTenantMembershipParams struct {
	TenantID    string
	PrincipalID string
	Issuer      string
	Subject     string
	Email       string
	DisplayName string
	Role        auth.Role
	Actor       AuditActor
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

func (s *Store) UpsertOIDCProvider(ctx context.Context, params UpsertOIDCProviderParams) (auth.OIDCProvider, error) {
	if err := s.ready(); err != nil {
		return auth.OIDCProvider{}, err
	}
	if params.ID == "" {
		params.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(params.ID); err != nil {
		return auth.OIDCProvider{}, invalid("OIDC provider ID must be a UUID")
	}
	if _, err := uuid.Parse(params.TenantID); err != nil {
		return auth.OIDCProvider{}, invalid("OIDC tenant ID must be a UUID")
	}
	params.Issuer = strings.TrimSpace(params.Issuer)
	params.ClientID = strings.TrimSpace(params.ClientID)
	params.JWKSURI = strings.TrimSpace(params.JWKSURI)
	if !validOIDCIssuer(params.Issuer) || params.ClientID == "" || (params.JWKSURI != "" && !validHTTPSURL(params.JWKSURI)) {
		return auth.OIDCProvider{}, invalid("OIDC issuer/client ID/JWKS URI are invalid")
	}
	normalizedDomains, err := normalizeDomains(params.AllowedDomains)
	if err != nil {
		return auth.OIDCProvider{}, err
	}
	params.AllowedDomains = normalizedDomains
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return auth.OIDCProvider{}, fmt.Errorf("postgres store: upsert OIDC provider: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	provider := auth.OIDCProvider{ID: params.ID, TenantID: params.TenantID, Issuer: params.Issuer,
		ClientID: params.ClientID, JWKSURI: params.JWKSURI, AllowedDomains: params.AllowedDomains, Enabled: params.Enabled}
	err = tx.QueryRow(ctx, `
INSERT INTO oidc_providers(id,tenant_id,issuer,client_id,jwks_uri,allowed_domains,enabled)
VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7)
ON CONFLICT (tenant_id,issuer) DO UPDATE SET
    client_id=excluded.client_id,jwks_uri=excluded.jwks_uri,
    allowed_domains=excluded.allowed_domains,enabled=excluded.enabled,updated_at=statement_timestamp()
RETURNING id,tenant_id,issuer,client_id,COALESCE(jwks_uri,''),allowed_domains,enabled`,
		provider.ID, provider.TenantID, provider.Issuer, provider.ClientID, provider.JWKSURI,
		provider.AllowedDomains, provider.Enabled).Scan(&provider.ID, &provider.TenantID, &provider.Issuer,
		&provider.ClientID, &provider.JWKSURI, &provider.AllowedDomains, &provider.Enabled)
	if err != nil {
		return auth.OIDCProvider{}, fmt.Errorf("postgres store: upsert OIDC provider: %w", err)
	}
	metadata, _ := json.Marshal(map[string]any{"issuer": provider.Issuer, "enabled": provider.Enabled})
	if _, err := appendAuditTx(ctx, tx, provider.TenantID, auditInput(params.Actor, "identity.oidc_provider.upsert", "oidc_provider", provider.ID, metadata)); err != nil {
		return auth.OIDCProvider{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.OIDCProvider{}, fmt.Errorf("postgres store: upsert OIDC provider: commit: %w", err)
	}
	return provider, nil
}

func (s *Store) OIDCProvider(ctx context.Context, tenantID, issuer string) (auth.OIDCProvider, error) {
	if err := s.ready(); err != nil {
		return auth.OIDCProvider{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil || strings.TrimSpace(issuer) == "" {
		return auth.OIDCProvider{}, invalid("OIDC tenant and issuer are required")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return auth.OIDCProvider{}, fmt.Errorf("postgres store: read OIDC provider: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var provider auth.OIDCProvider
	err = tx.QueryRow(ctx, `
SELECT id,tenant_id,issuer,client_id,COALESCE(jwks_uri,''),allowed_domains,enabled
FROM oidc_providers WHERE tenant_id=$1 AND issuer=$2`, tenantID, issuer).Scan(
		&provider.ID, &provider.TenantID, &provider.Issuer, &provider.ClientID,
		&provider.JWKSURI, &provider.AllowedDomains, &provider.Enabled)
	if err != nil {
		return auth.OIDCProvider{}, fmt.Errorf("postgres store: read OIDC provider: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.OIDCProvider{}, fmt.Errorf("postgres store: read OIDC provider: commit: %w", err)
	}
	return provider, nil
}

func (s *Store) SetTenantMembership(ctx context.Context, params SetTenantMembershipParams) (auth.Identity, error) {
	if err := s.ready(); err != nil {
		return auth.Identity{}, err
	}
	if params.PrincipalID == "" {
		params.PrincipalID = uuid.NewString()
	}
	if _, err := uuid.Parse(params.TenantID); err != nil {
		return auth.Identity{}, invalid("membership tenant ID must be a UUID")
	}
	if _, err := uuid.Parse(params.PrincipalID); err != nil || strings.TrimSpace(params.Issuer) == "" || strings.TrimSpace(params.Subject) == "" || !params.Role.Valid() {
		return auth.Identity{}, invalid("membership principal, issuer, subject, and role are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: set tenant membership: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	identity := auth.Identity{TenantID: params.TenantID, ActorType: "user", Issuer: strings.TrimSpace(params.Issuer),
		Subject: strings.TrimSpace(params.Subject), Email: strings.TrimSpace(params.Email),
		DisplayName: strings.TrimSpace(params.DisplayName), Role: params.Role}
	if err = lockTeam(ctx, tx, params.TenantID); err != nil {
		return auth.Identity{}, err
	}
	if params.Role != auth.RoleOwner {
		var lastOwner bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM principals p JOIN tenant_memberships m ON p.id=m.principal_id WHERE p.issuer=$1 AND p.subject=$2 AND m.tenant_id=$3 AND m.role='owner' AND m.enabled) AND (SELECT count(*) FROM tenant_memberships WHERE tenant_id=$3 AND role='owner' AND enabled)<=1`, identity.Issuer, identity.Subject, params.TenantID).Scan(&lastOwner)
		if err != nil {
			return auth.Identity{}, err
		}
		if lastOwner {
			return auth.Identity{}, ErrConflict
		}
	}
	err = tx.QueryRow(ctx, `
INSERT INTO principals(id,issuer,subject,email,display_name)
VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''))
ON CONFLICT (issuer,subject) DO UPDATE SET
    email=COALESCE(excluded.email,principals.email),
    display_name=COALESCE(excluded.display_name,principals.display_name),updated_at=statement_timestamp()
RETURNING id,COALESCE(email,''),COALESCE(display_name,'')`, params.PrincipalID, identity.Issuer,
		identity.Subject, identity.Email, identity.DisplayName).Scan(&identity.ActorID, &identity.Email, &identity.DisplayName)
	if err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: upsert principal: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO tenant_memberships(tenant_id,principal_id,role) VALUES($1,$2,$3)
ON CONFLICT (tenant_id,principal_id) DO UPDATE SET role=excluded.role,enabled=true,updated_at=statement_timestamp()`,
		identity.TenantID, identity.ActorID, string(identity.Role)); err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: upsert tenant membership: %w", err)
	}
	metadata, _ := json.Marshal(map[string]any{"role": identity.Role, "issuer": identity.Issuer, "subject": identity.Subject})
	if _, err := appendAuditTx(ctx, tx, identity.TenantID, auditInput(params.Actor, "identity.membership.set", "principal", identity.ActorID, metadata)); err != nil {
		return auth.Identity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: set tenant membership: commit: %w", err)
	}
	return identity, nil
}

func (s *Store) ResolveOIDCIdentity(ctx context.Context, tenantID string, claims auth.Claims) (auth.Identity, error) {
	if invite := auth.InvitationFromContext(ctx); invite != "" {
		if err := s.AcceptOIDCInvitation(ctx, tenantID, invite, claims); err != nil {
			return auth.Identity{}, err
		}
	}
	if err := s.ready(); err != nil {
		return auth.Identity{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil || claims.Issuer == "" || claims.Subject == "" {
		return auth.Identity{}, invalid("OIDC identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: resolve OIDC identity: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	identity := auth.Identity{TenantID: tenantID, ActorType: "user", Issuer: claims.Issuer, Subject: claims.Subject}
	var role string
	if err := tx.QueryRow(ctx, `
SELECT p.id,COALESCE(p.email,''),COALESCE(p.display_name,''),m.role
FROM principals p JOIN tenant_memberships m ON m.principal_id=p.id
WHERE p.issuer=$1 AND p.subject=$2 AND m.tenant_id=$3 AND m.enabled`, claims.Issuer, claims.Subject, tenantID).Scan(
		&identity.ActorID, &identity.Email, &identity.DisplayName, &role); err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: resolve OIDC membership: %w", err)
	}
	identity.Role = auth.Role(role)
	if !identity.Role.Valid() {
		return auth.Identity{}, errors.New("postgres store: invalid membership role")
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.Identity{}, fmt.Errorf("postgres store: resolve OIDC identity: commit: %w", err)
	}
	return identity, nil
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

func validOIDCIssuer(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func normalizeDomains(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "@/: ") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
			return nil, invalid("OIDC allowed domain is invalid")
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result, nil
}
