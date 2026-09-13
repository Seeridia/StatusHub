package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/audit"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

var tenantSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (s *Store) CreateTenant(ctx context.Context, params CreateTenantParams) (Tenant, error) {
	if err := s.ready(); err != nil {
		return Tenant{}, err
	}
	if params.ID == "" {
		params.ID = uuid.NewString()
	}
	params.Slug = strings.ToLower(strings.TrimSpace(params.Slug))
	params.Name = strings.TrimSpace(params.Name)
	if _, err := uuid.Parse(params.ID); err != nil || !tenantSlugPattern.MatchString(params.Slug) || params.Name == "" || len(params.Name) > 200 {
		return Tenant{}, invalid("tenant ID, slug, or name is invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Tenant{}, fmt.Errorf("postgres store: create tenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenant Tenant
	err = tx.QueryRow(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3)
ON CONFLICT DO NOTHING RETURNING id,slug,name`, params.ID, params.Slug, params.Name).Scan(&tenant.ID, &tenant.Slug, &tenant.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrConflict
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("postgres store: create tenant: %w", err)
	}
	metadata, _ := json.Marshal(map[string]string{"slug": tenant.Slug, "name": tenant.Name})
	if _, err := appendAuditTx(ctx, tx, tenant.ID, auditInput(params.Actor, "tenant.create", "tenant", tenant.ID, metadata)); err != nil {
		return Tenant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Tenant{}, fmt.Errorf("postgres store: create tenant: commit: %w", err)
	}
	return tenant, nil
}

func (s *Store) CreateSource(ctx context.Context, params CreateSourceParams) (SourceView, error) {
	if err := s.ready(); err != nil {
		return SourceView{}, err
	}
	if params.ID == "" {
		params.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(params.ID); err != nil {
		return SourceView{}, invalid("source ID must be a UUID")
	}
	if _, err := uuid.Parse(params.TenantID); err != nil {
		return SourceView{}, invalid("source tenant ID must be a UUID")
	}
	if _, err := uuid.Parse(params.VendorID); err != nil {
		return SourceView{}, invalid("source vendor ID must be a UUID")
	}
	if strings.TrimSpace(params.CanonicalURL) == "" || strings.TrimSpace(params.RequestedURL) == "" ||
		strings.TrimSpace(params.SourceType) == "" || strings.TrimSpace(params.AdapterName) == "" ||
		strings.TrimSpace(params.AdapterVersion) == "" || !validRegion(params.ActiveRegion) {
		return SourceView{}, invalid("source URL, type, adapter, version, and region are required")
	}
	if !params.Capabilities.ExpiresAt.After(time.Now().Add(-time.Minute)) || len(params.Capabilities.Endpoints) == 0 {
		return SourceView{}, invalid("source capabilities are missing or expired")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SourceView{}, fmt.Errorf("postgres store: create source: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// URL-derived identities are created atomically with the source. Existing
	// vendor metadata is never overwritten by tenant-supplied input.
	if params.AutoVendorSlug != "" && params.AutoVendorName != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO vendors(id,slug,name) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, params.VendorID, params.AutoVendorSlug, params.AutoVendorName); err != nil {
			return SourceView{}, fmt.Errorf("postgres store: create discovered vendor: %w", err)
		}
	}
	var item SourceView
	err = tx.QueryRow(ctx, `
INSERT INTO sources(id,tenant_id,vendor_id,requested_url,final_url,canonical_url,source_type,
                    adapter_name,adapter_version,next_poll_at)
SELECT $1,$2,v.id,$4,NULLIF($5,''),$6,$7,$8,$9,statement_timestamp()
FROM vendors v WHERE v.id=$3
ON CONFLICT DO NOTHING
RETURNING id,tenant_id,vendor_id,(SELECT slug FROM vendors WHERE id=vendor_id),
          (SELECT name FROM vendors WHERE id=vendor_id),requested_url,COALESCE(final_url,''),canonical_url,
          source_type,COALESCE(adapter_name,''),COALESCE(adapter_version,''),enabled,health_state,
          failure_streak,last_attempt_at,last_success_at,next_poll_at,updated_at`,
		params.ID, params.TenantID, params.VendorID, params.RequestedURL, params.FinalURL,
		params.CanonicalURL, params.SourceType, params.AdapterName, params.AdapterVersion).Scan(
		&item.ID, &item.TenantID, &item.VendorID, &item.VendorSlug, &item.VendorName,
		&item.RequestedURL, &item.FinalURL, &item.CanonicalURL, &item.SourceType, &item.AdapterName,
		&item.AdapterVersion, &item.Enabled, &item.HealthState, &item.FailureStreak,
		&item.LastAttemptAt, &item.LastSuccessAt, &item.NextPollAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingTenant string
		lookupErr := tx.QueryRow(ctx, `SELECT COALESCE(tenant_id::text,'') FROM sources WHERE id=$1`, params.ID).Scan(&existingTenant)
		if lookupErr == nil && existingTenant == params.TenantID {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return SourceView{}, fmt.Errorf("postgres store: create existing source: commit: %w", commitErr)
			}
			return s.Source(ctx, params.TenantID, params.ID)
		}
		return SourceView{}, ErrConflict
	}
	if err != nil {
		return SourceView{}, fmt.Errorf("postgres store: create source: %w", err)
	}
	ownershipTag, err := tx.Exec(ctx, `
INSERT INTO source_ownership(source_id,home_region,active_region,changed_by,change_reason)
SELECT $1,$2,$2,$3,'created by management API' FROM regions WHERE id=$2 AND enabled`,
		params.ID, params.ActiveRegion, auditActorID(params.Actor))
	if err != nil {
		return SourceView{}, fmt.Errorf("postgres store: create source ownership: %w", err)
	}
	if ownershipTag.RowsAffected() != 1 {
		return SourceView{}, invalid("source region does not exist or is disabled")
	}
	for kind, capability := range params.Capabilities.Endpoints {
		endpointURL, err := resolveCapabilityURL(params.CanonicalURL, capability.Path)
		if err != nil {
			return SourceView{}, err
		}
		metadata, _ := json.Marshal(capability)
		var historyWindow any
		if capability.HistoryWindow > 0 {
			historyWindow = capability.HistoryWindow.String()
		}
		var maximumBody any
		if capability.MaximumBodyBytes > 0 {
			maximumBody = capability.MaximumBodyBytes
		}
		_, err = tx.Exec(ctx, `
INSERT INTO source_capabilities(source_id,resource_kind,endpoint_url,engine,engine_version,adapter_version,
    confidence,authoritative,completeness,supports_etag,supports_last_modified,supports_pagination,
    requires_auth,history_window,max_body_bytes,schema_hash,expires_at,metadata)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::interval,$15,NULLIF($16,''),$17,$18)`,
			params.ID, string(kind), endpointURL, params.Capabilities.Engine, params.Capabilities.Version,
			params.AdapterVersion, params.Capabilities.Confidence, len(capability.AuthoritativeFor) > 0,
			string(capability.Completeness), capability.Cache.SupportsETag, capability.Cache.SupportsLastModified,
			capability.Pagination.Kind != domain.PaginationNone, capability.Authentication != "" && capability.Authentication != "none",
			historyWindow, maximumBody, params.Capabilities.SchemaHash, params.Capabilities.ExpiresAt, metadata)
		if err != nil {
			return SourceView{}, fmt.Errorf("postgres store: insert source capability %s: %w", kind, err)
		}
	}
	metadata, _ := json.Marshal(map[string]any{"vendor_id": params.VendorID, "canonical_url": params.CanonicalURL})
	if _, err := appendAuditTx(ctx, tx, params.TenantID, auditInput(params.Actor, "source.create", "source", params.ID, metadata)); err != nil {
		return SourceView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceView{}, fmt.Errorf("postgres store: create source: commit: %w", err)
	}
	item.Collection = sourceCollection(item, nil, time.Now().UTC())
	return item, nil
}

func resolveCapabilityURL(baseValue, path string) (string, error) {
	base, err := url.Parse(baseValue)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", invalid("canonical source URL is invalid")
	}
	reference, err := url.Parse(path)
	if err != nil {
		return "", invalid("capability path is invalid")
	}
	return base.ResolveReference(reference).String(), nil
}

func (s *Store) SetSourceEnabled(ctx context.Context, tenantID, sourceID string, enabled bool, actor AuditActor) (SourceView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return SourceView{}, invalid("source tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SourceView{}, fmt.Errorf("postgres store: update source: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE sources SET enabled=$3,updated_at=statement_timestamp() WHERE id=$2 AND tenant_id=$1`, tenantID, sourceID, enabled)
	if err != nil {
		return SourceView{}, fmt.Errorf("postgres store: update source: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return SourceView{}, ErrNotFound
	}
	metadata, _ := json.Marshal(map[string]bool{"enabled": enabled})
	if _, err := appendAuditTx(ctx, tx, tenantID, auditInput(actor, "source.update", "source", sourceID, metadata)); err != nil {
		return SourceView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceView{}, fmt.Errorf("postgres store: update source: commit: %w", err)
	}
	return s.Source(ctx, tenantID, sourceID)
}

func (s *Store) CreateSubscription(ctx context.Context, params CreateSubscriptionParams) (SubscriptionView, error) {
	if err := validateSubscriptionParams(params); err != nil {
		return SubscriptionView{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: create subscription: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item SubscriptionView
	err = tx.QueryRow(ctx, `
INSERT INTO subscriptions(id,tenant_id,name,enabled,rule)
VALUES($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING
RETURNING id,tenant_id,name,enabled,rule_version,rule,created_at,updated_at`,
		params.ID, params.TenantID, params.Name, params.Enabled, params.Rule).Scan(
		&item.ID, &item.TenantID, &item.Name, &item.Enabled, &item.RuleVersion,
		&item.Rule, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingTenant string
		lookupErr := tx.QueryRow(ctx, `SELECT tenant_id FROM subscriptions WHERE id=$1`, params.ID).Scan(&existingTenant)
		if lookupErr == nil && existingTenant == params.TenantID {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return SubscriptionView{}, fmt.Errorf("postgres store: existing subscription: commit: %w", commitErr)
			}
			return s.Subscription(ctx, params.TenantID, params.ID)
		}
		return SubscriptionView{}, ErrConflict
	}
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: create subscription: %w", err)
	}
	if err := replaceSubscriptionRelations(ctx, tx, params); err != nil {
		return SubscriptionView{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"name": params.Name, "endpoint_count": len(params.EndpointIDs)})
	if _, err := appendAuditTx(ctx, tx, params.TenantID, auditInput(params.Actor, "subscription.create", "subscription", params.ID, metadata)); err != nil {
		return SubscriptionView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: create subscription: commit: %w", err)
	}
	return s.Subscription(ctx, params.TenantID, params.ID)
}

func (s *Store) UpdateSubscription(ctx context.Context, params UpdateSubscriptionParams) (SubscriptionView, error) {
	if err := validateSubscriptionParams(params.CreateSubscriptionParams); err != nil {
		return SubscriptionView{}, err
	}
	if params.ExpectedRuleVersion <= 0 {
		return SubscriptionView{}, invalid("expected subscription rule version is required")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: update subscription: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var version int
	err = tx.QueryRow(ctx, `
UPDATE subscriptions SET name=$3,enabled=$4,rule=$5,rule_version=rule_version+1,updated_at=statement_timestamp()
WHERE id=$1 AND tenant_id=$2 AND rule_version=$6 RETURNING rule_version`,
		params.ID, params.TenantID, params.Name, params.Enabled, params.Rule, params.ExpectedRuleVersion).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE id=$1 AND tenant_id=$2)`, params.ID, params.TenantID).Scan(&exists)
		if exists {
			return SubscriptionView{}, ErrConflict
		}
		return SubscriptionView{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: update subscription: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM subscription_scopes WHERE subscription_id=$1`, params.ID); err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: clear subscription scopes: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM subscription_endpoints WHERE subscription_id=$1`, params.ID); err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: clear subscription endpoints: %w", err)
	}
	if err := replaceSubscriptionRelations(ctx, tx, params.CreateSubscriptionParams); err != nil {
		return SubscriptionView{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"rule_version": version})
	if _, err := appendAuditTx(ctx, tx, params.TenantID, auditInput(params.Actor, "subscription.update", "subscription", params.ID, metadata)); err != nil {
		return SubscriptionView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: update subscription: commit: %w", err)
	}
	return s.Subscription(ctx, params.TenantID, params.ID)
}

func (s *Store) SetSubscriptionEnabled(ctx context.Context, tenantID, subscriptionID string, enabled bool, actor AuditActor) (SubscriptionView, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: disable subscription: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE subscriptions SET enabled=$3,updated_at=statement_timestamp() WHERE tenant_id=$1 AND id=$2`, tenantID, subscriptionID, enabled)
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: disable subscription: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return SubscriptionView{}, ErrNotFound
	}
	metadata, _ := json.Marshal(map[string]bool{"enabled": enabled})
	if _, err := appendAuditTx(ctx, tx, tenantID, auditInput(actor, "subscription.enable", "subscription", subscriptionID, metadata)); err != nil {
		return SubscriptionView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: disable subscription: commit: %w", err)
	}
	return s.Subscription(ctx, tenantID, subscriptionID)
}

func validateSubscriptionParams(params CreateSubscriptionParams) error {
	if params.ID == "" {
		return invalid("subscription ID is required")
	}
	if _, err := uuid.Parse(params.ID); err != nil {
		return invalid("subscription ID must be a UUID")
	}
	if _, err := uuid.Parse(params.TenantID); err != nil {
		return invalid("subscription tenant ID must be a UUID")
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" || len(params.Name) > 200 || len(params.Rule) == 0 || !json.Valid(params.Rule) {
		return invalid("subscription name or rule is invalid")
	}
	if len(params.Scopes) > 200 || len(params.EndpointIDs) == 0 || len(params.EndpointIDs) > 100 {
		return invalid("subscription scope or endpoint count is invalid")
	}
	for _, endpointID := range params.EndpointIDs {
		if _, err := uuid.Parse(endpointID); err != nil {
			return invalid("subscription endpoint ID must be a UUID")
		}
	}
	return nil
}

func replaceSubscriptionRelations(ctx context.Context, tx pgx.Tx, params CreateSubscriptionParams) error {
	for _, scope := range params.Scopes {
		if scope.VendorID != "" {
			if _, err := uuid.Parse(scope.VendorID); err != nil {
				return invalid("subscription scope vendor ID must be a UUID")
			}
		}
		if scope.ComponentID != "" {
			if _, err := uuid.Parse(scope.ComponentID); err != nil {
				return invalid("subscription scope component ID must be a UUID")
			}
		}
		if len(scope.ComponentKey) > 512 || len(scope.Tag) > 128 || len(scope.EventKind) > 128 {
			return invalid("subscription scope value is too long")
		}
		if scope.EventKind != "" && !domain.EventKind(scope.EventKind).Valid() {
			return invalid("subscription scope event kind is invalid")
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO subscription_scopes(id,subscription_id,vendor_id,component_id,component_key,tag,event_kind)
VALUES(gen_random_uuid(),$1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''))`,
			params.ID, scope.VendorID, scope.ComponentID, scope.ComponentKey, scope.Tag, scope.EventKind); err != nil {
			return fmt.Errorf("postgres store: insert subscription scope: %w", err)
		}
	}
	for _, endpointID := range params.EndpointIDs {
		tag, err := tx.Exec(ctx, `
INSERT INTO subscription_endpoints(subscription_id,endpoint_id)
SELECT $1,e.id FROM endpoints e WHERE e.id=$2 AND e.tenant_id=$3
ON CONFLICT DO NOTHING`, params.ID, endpointID, params.TenantID)
		if err != nil {
			return fmt.Errorf("postgres store: insert subscription endpoint: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return invalid("subscription endpoint is not owned by the tenant")
		}
	}
	return nil
}

func (s *Store) CreateEndpoint(ctx context.Context, params CreateEndpointParams) (EndpointView, error) {
	if err := validateEndpointParams(params); err != nil {
		return EndpointView{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: create endpoint: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item EndpointView
	err = tx.QueryRow(ctx, `
INSERT INTO endpoints(id,tenant_id,channel,name,enabled,encrypted_config,key_id,secret_version,rate_limit_config)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING
RETURNING id,tenant_id,channel,name,enabled,key_id,secret_version,health_state,rate_limit_config,created_at,updated_at`,
		params.ID, params.TenantID, params.Channel, params.Name, params.Enabled, params.EncryptedConfig,
		params.KeyID, params.SecretVersion, params.RateLimits).Scan(
		&item.ID, &item.TenantID, &item.Channel, &item.Name, &item.Enabled, &item.KeyID,
		&item.SecretVersion, &item.HealthState, &item.RateLimits, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingTenant string
		lookupErr := tx.QueryRow(ctx, `SELECT tenant_id FROM endpoints WHERE id=$1`, params.ID).Scan(&existingTenant)
		if lookupErr == nil && existingTenant == params.TenantID {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return EndpointView{}, fmt.Errorf("postgres store: existing endpoint: commit: %w", commitErr)
			}
			existing, readErr := s.Endpoint(ctx, params.TenantID, params.ID, false)
			return existing.EndpointView, readErr
		}
		return EndpointView{}, ErrConflict
	}
	if err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: create endpoint: %w", err)
	}
	metadata, _ := json.Marshal(map[string]any{"channel": params.Channel, "name": params.Name})
	if _, err := appendAuditTx(ctx, tx, params.TenantID, auditInput(params.Actor, "endpoint.create", "endpoint", params.ID, metadata)); err != nil {
		return EndpointView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: create endpoint: commit: %w", err)
	}
	return item, nil
}

func (s *Store) UpdateEndpoint(ctx context.Context, params CreateEndpointParams, expectedSecretVersion int) (EndpointView, error) {
	if err := validateEndpointParams(params); err != nil {
		return EndpointView{}, err
	}
	if expectedSecretVersion <= 0 || params.SecretVersion != expectedSecretVersion+1 {
		return EndpointView{}, invalid("endpoint secret version must increment by one")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: update endpoint: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item EndpointView
	err = tx.QueryRow(ctx, `
UPDATE endpoints SET channel=$3,name=$4,enabled=$5,encrypted_config=$6,key_id=$7,
    secret_version=$8,rate_limit_config=$9,health_state='unknown',updated_at=statement_timestamp()
WHERE id=$1 AND tenant_id=$2 AND secret_version=$10
RETURNING id,tenant_id,channel,name,enabled,key_id,secret_version,health_state,rate_limit_config,created_at,updated_at`,
		params.ID, params.TenantID, params.Channel, params.Name, params.Enabled, params.EncryptedConfig,
		params.KeyID, params.SecretVersion, params.RateLimits, expectedSecretVersion).Scan(
		&item.ID, &item.TenantID, &item.Channel, &item.Name, &item.Enabled, &item.KeyID,
		&item.SecretVersion, &item.HealthState, &item.RateLimits, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM endpoints WHERE id=$1 AND tenant_id=$2)`, params.ID, params.TenantID).Scan(&exists)
		if exists {
			return EndpointView{}, ErrConflict
		}
		return EndpointView{}, ErrNotFound
	}
	if err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: update endpoint: %w", err)
	}
	metadata, _ := json.Marshal(map[string]any{"secret_version": params.SecretVersion, "channel": params.Channel})
	if _, err := appendAuditTx(ctx, tx, params.TenantID, auditInput(params.Actor, "endpoint.rotate", "endpoint", params.ID, metadata)); err != nil {
		return EndpointView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: update endpoint: commit: %w", err)
	}
	return item, nil
}

func (s *Store) SetEndpointEnabled(ctx context.Context, tenantID, endpointID string, enabled bool, actor AuditActor) (EndpointView, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: enable endpoint: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE endpoints SET enabled=$3,updated_at=statement_timestamp() WHERE tenant_id=$1 AND id=$2`, tenantID, endpointID, enabled)
	if err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: enable endpoint: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return EndpointView{}, ErrNotFound
	}
	metadata, _ := json.Marshal(map[string]bool{"enabled": enabled})
	if _, err := appendAuditTx(ctx, tx, tenantID, auditInput(actor, "endpoint.enable", "endpoint", endpointID, metadata)); err != nil {
		return EndpointView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointView{}, fmt.Errorf("postgres store: enable endpoint: commit: %w", err)
	}
	item, err := s.Endpoint(ctx, tenantID, endpointID, false)
	return item.EndpointView, err
}

func validateEndpointParams(params CreateEndpointParams) error {
	if _, err := uuid.Parse(params.ID); err != nil {
		return invalid("endpoint ID must be a UUID")
	}
	if _, err := uuid.Parse(params.TenantID); err != nil {
		return invalid("endpoint tenant ID must be a UUID")
	}
	if strings.TrimSpace(params.Channel) == "" || strings.TrimSpace(params.Name) == "" || len(params.Name) > 200 ||
		len(params.EncryptedConfig) == 0 || strings.TrimSpace(params.KeyID) == "" || params.SecretVersion <= 0 {
		return invalid("endpoint channel, name, encrypted config, key, and version are required")
	}
	if len(params.EncryptedConfig) > 256<<10 {
		return invalid("endpoint encrypted config exceeds 256 KiB")
	}
	if len(params.RateLimits) == 0 {
		params.RateLimits = json.RawMessage(`{}`)
	}
	if !json.Valid(params.RateLimits) {
		return invalid("endpoint rate limit configuration is invalid")
	}
	return nil
}

func (s *Store) ReplayTenantDelivery(ctx context.Context, tenantID, deliveryID string, at time.Time, actor AuditActor) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("postgres store: replay tenant delivery: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var allowed bool
	err = tx.QueryRow(ctx, `
SELECT d.status='dead_letter' AND s.enabled AND ep.enabled
       AND d.superseded_at IS NULL AND (d.expires_at IS NULL OR d.expires_at>$3)
       AND EXISTS (SELECT 1 FROM delivery_attempts da WHERE da.delivery_id=d.id AND da.secret_version=ep.secret_version)
FROM deliveries d JOIN subscriptions s ON s.id=d.subscription_id JOIN endpoints ep ON ep.id=d.endpoint_id
WHERE d.id=$2 AND s.tenant_id=$1 AND ep.tenant_id=$1 FOR UPDATE OF d`, tenantID, deliveryID, at).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("postgres store: inspect tenant delivery: %w", err)
	}
	if !allowed {
		return false, ErrReplayNotAllowed
	}
	tag, err := tx.Exec(ctx, `
UPDATE deliveries SET status='queued',next_attempt_at=$2,replay_count=replay_count+1,
    dead_lettered_at=NULL,dead_letter_reason=NULL,last_error_class=NULL,last_error_summary=NULL,
    lease_owner=NULL,lease_token=NULL,lease_until=NULL,updated_at=statement_timestamp()
WHERE id=$1 AND status='dead_letter'`, deliveryID, at)
	if err != nil || tag.RowsAffected() != 1 {
		return false, ErrReplayNotAllowed
	}
	if _, err := appendAuditTx(ctx, tx, tenantID, auditInput(actor, "delivery.replay", "delivery", deliveryID, json.RawMessage(`{}`))); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres store: replay tenant delivery: commit: %w", err)
	}
	return true, nil
}

func (s *Store) BeginIdempotency(ctx context.Context, record IdempotencyRecord) (IdempotencyRecord, bool, error) {
	if _, err := uuid.Parse(record.TenantID); err != nil || strings.TrimSpace(record.Key) == "" || len(record.Key) > 128 ||
		strings.TrimSpace(record.Method) == "" || strings.TrimSpace(record.Route) == "" || len(record.RequestHash) != sha256.Size {
		return IdempotencyRecord{}, false, invalid("idempotency record is invalid")
	}
	if record.ResourceID == "" {
		record.ResourceID = uuid.NewString()
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("postgres store: begin idempotency: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
INSERT INTO api_idempotency_keys(tenant_id,idempotency_key,method,route,request_hash,resource_id)
VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, record.TenantID, record.Key,
		record.Method, record.Route, record.RequestHash, record.ResourceID)
	if err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("postgres store: begin idempotency: %w", err)
	}
	if tag.RowsAffected() == 1 {
		if err := tx.Commit(ctx); err != nil {
			return IdempotencyRecord{}, false, fmt.Errorf("postgres store: begin idempotency: commit: %w", err)
		}
		record.State = "processing"
		record.LockedUntil = time.Now().UTC().Add(30 * time.Second)
		return record, true, nil
	}
	var existing IdempotencyRecord
	err = tx.QueryRow(ctx, `
SELECT tenant_id,idempotency_key,method,route,request_hash,state,COALESCE(resource_id::text,''),
       COALESCE(response_status,0),COALESCE(response_body,'null'::jsonb),locked_until,expires_at
FROM api_idempotency_keys WHERE tenant_id=$1 AND idempotency_key=$2 FOR UPDATE`, record.TenantID, record.Key).Scan(
		&existing.TenantID, &existing.Key, &existing.Method, &existing.Route, &existing.RequestHash,
		&existing.State, &existing.ResourceID, &existing.ResponseStatus, &existing.ResponseBody, &existing.LockedUntil, &existing.ExpiresAt)
	if err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("postgres store: read idempotency: %w", err)
	}
	if !existing.ExpiresAt.After(time.Now().UTC()) {
		_, err := tx.Exec(ctx, `
UPDATE api_idempotency_keys SET method=$3,route=$4,request_hash=$5,state='processing',resource_id=$6,
    response_status=NULL,response_body=NULL,locked_until=statement_timestamp()+interval '30 seconds',
    expires_at=statement_timestamp()+interval '24 hours',created_at=statement_timestamp(),updated_at=statement_timestamp()
WHERE tenant_id=$1 AND idempotency_key=$2`, record.TenantID, record.Key, record.Method, record.Route,
			record.RequestHash, record.ResourceID)
		if err != nil {
			return IdempotencyRecord{}, false, fmt.Errorf("postgres store: recycle idempotency: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return IdempotencyRecord{}, false, fmt.Errorf("postgres store: recycle idempotency: commit: %w", err)
		}
		record.State = "processing"
		return record, true, nil
	}
	if existing.Method != record.Method || existing.Route != record.Route || !bytes.Equal(existing.RequestHash, record.RequestHash) {
		return IdempotencyRecord{}, false, ErrIdempotencyConflict
	}
	if existing.State == "completed" {
		if err := tx.Commit(ctx); err != nil {
			return IdempotencyRecord{}, false, fmt.Errorf("postgres store: read completed idempotency: commit: %w", err)
		}
		return existing, false, nil
	}
	if existing.LockedUntil.After(time.Now().UTC()) {
		return IdempotencyRecord{}, false, ErrIdempotencyBusy
	}
	if _, err := tx.Exec(ctx, `
UPDATE api_idempotency_keys SET locked_until=statement_timestamp()+interval '30 seconds',updated_at=statement_timestamp()
WHERE tenant_id=$1 AND idempotency_key=$2`, record.TenantID, record.Key); err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("postgres store: reclaim idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return IdempotencyRecord{}, false, fmt.Errorf("postgres store: reclaim idempotency: commit: %w", err)
	}
	return existing, true, nil
}

func (s *Store) CompleteIdempotency(ctx context.Context, tenantID, key string, status int, body json.RawMessage) error {
	if status < 100 || status > 599 || len(body) == 0 || !json.Valid(body) {
		return invalid("idempotency response is invalid")
	}
	tag, err := s.db.Exec(ctx, `
UPDATE api_idempotency_keys SET state='completed',response_status=$3,response_body=$4,
    locked_until=statement_timestamp(),updated_at=statement_timestamp()
WHERE tenant_id=$1 AND idempotency_key=$2 AND state='processing'`, tenantID, key, status, body)
	if err != nil {
		return fmt.Errorf("postgres store: complete idempotency: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) CreateEndpointTest(ctx context.Context, tenantID, endpointID, jobID string, actor AuditActor) (EndpointTestJob, error) {
	if jobID == "" {
		jobID = uuid.NewString()
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EndpointTestJob{}, fmt.Errorf("postgres store: create endpoint test: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job EndpointTestJob
	err = tx.QueryRow(ctx, `
INSERT INTO endpoint_test_jobs(id,tenant_id,endpoint_id)
SELECT $1,$2,e.id FROM endpoints e WHERE e.id=$3 AND e.tenant_id=$2 AND e.enabled
ON CONFLICT (id) DO NOTHING
RETURNING id,tenant_id,endpoint_id,status,attempt_count,COALESCE(provider_message_id,''),http_status,
          COALESCE(error_class,''),COALESCE(error_summary,''),created_at,started_at,finished_at`,
		jobID, tenantID, endpointID).Scan(&job.ID, &job.TenantID, &job.EndpointID, &job.Status,
		&job.AttemptCount, &job.ProviderMessageID, &job.HTTPStatus, &job.ErrorClass, &job.ErrorSummary,
		&job.CreatedAt, &job.StartedAt, &job.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
SELECT id,tenant_id,endpoint_id,status,attempt_count,COALESCE(provider_message_id,''),http_status,
       COALESCE(error_class,''),COALESCE(error_summary,''),created_at,started_at,finished_at
FROM endpoint_test_jobs WHERE id=$1 AND tenant_id=$2 AND endpoint_id=$3`, jobID, tenantID, endpointID).Scan(
			&job.ID, &job.TenantID, &job.EndpointID, &job.Status, &job.AttemptCount, &job.ProviderMessageID,
			&job.HTTPStatus, &job.ErrorClass, &job.ErrorSummary, &job.CreatedAt, &job.StartedAt, &job.FinishedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return EndpointTestJob{}, ErrNotFound
		}
		if err != nil {
			return EndpointTestJob{}, fmt.Errorf("postgres store: read existing endpoint test: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return EndpointTestJob{}, fmt.Errorf("postgres store: existing endpoint test: commit: %w", err)
		}
		return job, nil
	}
	if err != nil {
		return EndpointTestJob{}, fmt.Errorf("postgres store: create endpoint test: %w", err)
	}
	if _, err := appendAuditTx(ctx, tx, tenantID, auditInput(actor, "endpoint.test.enqueue", "endpoint_test", jobID, json.RawMessage(`{}`))); err != nil {
		return EndpointTestJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointTestJob{}, fmt.Errorf("postgres store: create endpoint test: commit: %w", err)
	}
	return job, nil
}

func (s *Store) EndpointTest(ctx context.Context, tenantID, jobID string) (EndpointTestJob, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return EndpointTestJob{}, fmt.Errorf("postgres store: read endpoint test: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job EndpointTestJob
	err = tx.QueryRow(ctx, `
SELECT id,tenant_id,endpoint_id,status,attempt_count,COALESCE(provider_message_id,''),http_status,
       COALESCE(error_class,''),COALESCE(error_summary,''),created_at,started_at,finished_at
FROM endpoint_test_jobs WHERE id=$2 AND tenant_id=$1`, tenantID, jobID).Scan(
		&job.ID, &job.TenantID, &job.EndpointID, &job.Status, &job.AttemptCount, &job.ProviderMessageID,
		&job.HTTPStatus, &job.ErrorClass, &job.ErrorSummary, &job.CreatedAt, &job.StartedAt, &job.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EndpointTestJob{}, ErrNotFound
	}
	if err != nil {
		return EndpointTestJob{}, fmt.Errorf("postgres store: read endpoint test: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointTestJob{}, fmt.Errorf("postgres store: read endpoint test: commit: %w", err)
	}
	return job, nil
}

func (s *Store) ClaimEndpointTests(ctx context.Context, owner string, limit int, leaseDuration time.Duration) ([]EndpointTestLease, error) {
	if strings.TrimSpace(owner) == "" || limit <= 0 || limit > 100 || leaseDuration <= 0 {
		return nil, invalid("endpoint test claim arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim endpoint tests: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH claimed AS (
  SELECT id FROM endpoint_test_jobs
  WHERE status='queued' OR (status='running' AND lease_until<=statement_timestamp())
  ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT $1
), updated AS (
  UPDATE endpoint_test_jobs j SET status='running',lease_owner=$2,lease_token=gen_random_uuid(),
      lease_until=statement_timestamp()+($3::double precision*interval '1 microsecond'),
      attempt_count=attempt_count+1,started_at=COALESCE(started_at,statement_timestamp())
  FROM claimed WHERE j.id=claimed.id RETURNING j.*
)
SELECT u.id,u.tenant_id,u.endpoint_id,u.status,u.attempt_count,COALESCE(u.provider_message_id,''),u.http_status,
       COALESCE(u.error_class,''),COALESCE(u.error_summary,''),u.created_at,u.started_at,u.finished_at,
       u.lease_token,e.channel,e.key_id,e.secret_version,e.encrypted_config
FROM updated u JOIN endpoints e ON e.id=u.endpoint_id AND e.tenant_id=u.tenant_id AND e.enabled`,
		limit, owner, leaseDuration.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("postgres store: claim endpoint tests: %w", err)
	}
	defer rows.Close()
	result := make([]EndpointTestLease, 0, limit)
	for rows.Next() {
		var lease EndpointTestLease
		if err := rows.Scan(&lease.ID, &lease.TenantID, &lease.EndpointID, &lease.Status, &lease.AttemptCount,
			&lease.ProviderMessageID, &lease.HTTPStatus, &lease.ErrorClass, &lease.ErrorSummary, &lease.CreatedAt,
			&lease.StartedAt, &lease.FinishedAt, &lease.LeaseToken, &lease.Channel, &lease.KeyID,
			&lease.SecretVersion, &lease.EncryptedConfig); err != nil {
			return nil, fmt.Errorf("postgres store: scan endpoint test lease: %w", err)
		}
		result = append(result, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan endpoint test leases: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: claim endpoint tests: commit: %w", err)
	}
	return result, nil
}

func (s *Store) CompleteEndpointTest(ctx context.Context, params CompleteEndpointTestParams) error {
	status := "failed"
	if params.Succeeded {
		status = "succeeded"
	}
	tag, err := s.db.Exec(ctx, `
UPDATE endpoint_test_jobs SET status=$3,provider_message_id=NULLIF($4,''),http_status=NULLIF($5,0),
    error_class=NULLIF($6,''),error_summary=NULLIF($7,''),finished_at=statement_timestamp(),
    lease_owner=NULL,lease_token=NULL,lease_until=NULL
WHERE id=$1 AND lease_token=$2 AND status='running'`, params.ID, params.LeaseToken, status,
		params.ProviderMessageID, params.HTTPStatus, params.ErrorClass, params.ErrorSummary)
	if err != nil {
		return fmt.Errorf("postgres store: complete endpoint test: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func auditActorID(actor AuditActor) string {
	if strings.TrimSpace(actor.ID) != "" {
		return actor.ID
	}
	return "statusmon-api"
}

func appendControlPlaneAudit(ctx context.Context, s *Store, tenantID string, actor AuditActor, action, resourceType, resourceID string, metadata json.RawMessage) error {
	_, err := s.AppendAuditEvent(ctx, tenantID, audit.AppendInput{
		OccurredAt: time.Now().UTC(), ActorType: actor.Type, ActorID: auditActorID(actor), Action: action,
		ResourceType: resourceType, ResourceID: resourceID, Outcome: "success", RequestID: actor.RequestID,
		Metadata: metadata,
	})
	return err
}
