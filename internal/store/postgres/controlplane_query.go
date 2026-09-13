package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound            = errors.New("postgres store: resource not found")
	ErrConflict            = errors.New("postgres store: resource conflict")
	ErrIdempotencyConflict = errors.New("postgres store: idempotency key reused with a different request")
	ErrIdempotencyBusy     = errors.New("postgres store: idempotency request is still processing")
)

func (s *Store) ResolveTenant(ctx context.Context, key string) (Tenant, error) {
	if err := s.ready(); err != nil {
		return Tenant{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return Tenant{}, invalid("tenant key is required")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Tenant{}, fmt.Errorf("postgres store: resolve tenant: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenant Tenant
	err = tx.QueryRow(ctx, `SELECT id,slug,name FROM tenants WHERE id::text=$1 OR slug=$1`, key).Scan(&tenant.ID, &tenant.Slug, &tenant.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("postgres store: resolve tenant: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Tenant{}, fmt.Errorf("postgres store: resolve tenant: commit: %w", err)
	}
	return tenant, nil
}

func (s *Store) ListOIDCProviders(ctx context.Context, tenantID string) ([]auth.OIDCProvider, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, invalid("OIDC tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list OIDC providers: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
SELECT id,tenant_id,issuer,client_id,COALESCE(jwks_uri,''),allowed_domains,enabled
FROM oidc_providers WHERE tenant_id=$1 AND enabled ORDER BY issuer`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list OIDC providers: %w", err)
	}
	defer rows.Close()
	providers := make([]auth.OIDCProvider, 0)
	for rows.Next() {
		var provider auth.OIDCProvider
		if err := rows.Scan(&provider.ID, &provider.TenantID, &provider.Issuer, &provider.ClientID,
			&provider.JWKSURI, &provider.AllowedDomains, &provider.Enabled); err != nil {
			return nil, fmt.Errorf("postgres store: scan OIDC provider: %w", err)
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan OIDC providers: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list OIDC providers: commit: %w", err)
	}
	return providers, nil
}

func (s *Store) ListVendorStatuses(ctx context.Context, tenantID string) ([]VendorStatus, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, invalid("vendor status tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list vendor statuses: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
WITH visible_sources AS (
  SELECT * FROM sources WHERE tenant_id IS NULL OR tenant_id=$1
), incident_stats AS (
  SELECT s.vendor_id,
         count(*) FILTER (WHERE i.canonical_phase <> 'resolved') AS active_incidents,
         max(CASE i.canonical_impact WHEN 'critical' THEN 4 WHEN 'major' THEN 3 WHEN 'minor' THEN 2 ELSE 0 END)
           FILTER (WHERE i.canonical_phase <> 'resolved') AS incident_rank,
         max(i.observed_at) AS last_observed_at
  FROM visible_sources s LEFT JOIN incidents i ON i.source_id=s.id GROUP BY s.vendor_id
), component_stats AS (
  SELECT s.vendor_id,
         max(CASE c.canonical_status WHEN 'major_outage' THEN 4 WHEN 'partial_outage' THEN 3 WHEN 'degraded' THEN 2 WHEN 'under_maintenance' THEN 1 ELSE 0 END) AS component_rank,
         max(c.observed_at) AS last_observed_at
  FROM visible_sources s LEFT JOIN components c ON c.source_id=s.id GROUP BY s.vendor_id
), source_stats AS (
  SELECT vendor_id,count(*) AS source_count,max(last_success_at) AS last_success_at,
         bool_or(health_state='degraded') AS degraded
  FROM visible_sources GROUP BY vendor_id
)
SELECT v.id,v.slug,v.name,COALESCE(v.canonical_domain,''),v.aliases,
       CASE greatest(COALESCE(i.incident_rank,0),COALESCE(c.component_rank,0))
         WHEN 4 THEN 'major_outage' WHEN 3 THEN 'partial_outage' WHEN 2 THEN 'degraded'
         WHEN 1 THEN 'under_maintenance'
         ELSE CASE WHEN greatest(i.last_observed_at,c.last_observed_at) IS NULL THEN 'unknown' ELSE 'operational' END END,
       COALESCE(i.active_incidents,0),COALESCE(ss.source_count,0),
       greatest(i.last_observed_at,c.last_observed_at),ss.last_success_at,
       CASE WHEN COALESCE(ss.source_count,0)=0 THEN 'unknown' WHEN ss.degraded THEN 'degraded' ELSE 'healthy' END
FROM vendors v
LEFT JOIN incident_stats i ON i.vendor_id=v.id
LEFT JOIN component_stats c ON c.vendor_id=v.id
LEFT JOIN source_stats ss ON ss.vendor_id=v.id
ORDER BY v.name,v.id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list vendor statuses: %w", err)
	}
	defer rows.Close()
	result := make([]VendorStatus, 0)
	for rows.Next() {
		var item VendorStatus
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.CanonicalDomain, &item.Aliases,
			&item.Status, &item.ActiveIncidents, &item.VisibleSources, &item.LastObservedAt,
			&item.LastSuccessfulAt, &item.SourceHealthState); err != nil {
			return nil, fmt.Errorf("postgres store: scan vendor status: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan vendor statuses: %w", err)
	}
	rows.Close()
	collections, err := readCollections(ctx, tx, tenantID, nil)
	if err != nil {
		return nil, fmt.Errorf("postgres store: vendor collection status: %w", err)
	}
	byVendor := make(map[string][]SourceView)
	for _, row := range collections {
		byVendor[row.source.VendorID] = append(byVendor[row.source.VendorID], row.source)
	}
	for i := range result {
		result[i].Collection = vendorCollection(byVendor[result[i].ID], time.Now().UTC())
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list vendor statuses: commit: %w", err)
	}
	return result, nil
}

func (s *Store) VendorStatus(ctx context.Context, tenantID, vendorID string) (VendorStatus, error) {
	items, err := s.ListVendorStatuses(ctx, tenantID)
	if err != nil {
		return VendorStatus{}, err
	}
	for _, item := range items {
		if item.ID == vendorID || item.Slug == vendorID {
			return item, nil
		}
	}
	return VendorStatus{}, ErrNotFound
}

func (s *Store) ListSources(ctx context.Context, tenantID string, cursor *TimeCursor, limit int) ([]SourceView, error) {
	if err := validateList(tenantID, cursor, limit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list sources: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorTime, cursorID := cursorValues(cursor)
	rows, err := tx.Query(ctx, `
SELECT s.id,s.tenant_id,s.vendor_id,v.slug,v.name,s.requested_url,COALESCE(s.final_url,''),s.canonical_url,
       s.source_type,COALESCE(s.adapter_name,''),COALESCE(s.adapter_version,''),s.enabled,s.health_state,
       s.failure_streak,s.last_attempt_at,s.last_success_at,s.next_poll_at,s.updated_at
FROM sources s JOIN vendors v ON v.id=s.vendor_id
WHERE s.deleted_at IS NULL AND (s.tenant_id IS NULL OR s.tenant_id=$1)
  AND ($2::timestamptz IS NULL OR (s.updated_at,s.id)<($2,$3::uuid))
ORDER BY s.updated_at DESC,s.id DESC LIMIT $4`, tenantID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list sources: %w", err)
	}
	defer rows.Close()
	result := make([]SourceView, 0, limit)
	for rows.Next() {
		item, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan sources: %w", err)
	}
	rows.Close()
	ids := make([]string, 0, len(result))
	for _, item := range result {
		ids = append(ids, item.ID)
	}
	collections, err := readCollections(ctx, tx, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres store: source collection status: %w", err)
	}
	for i := range result {
		result[i].Collection = collections[result[i].ID].source.Collection
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list sources: commit: %w", err)
	}
	return result, nil
}

func (s *Store) Source(ctx context.Context, tenantID, sourceID string) (SourceView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return SourceView{}, invalid("source tenant ID must be a UUID")
	}
	if _, err := uuid.Parse(sourceID); err != nil {
		return SourceView{}, invalid("source ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SourceView{}, fmt.Errorf("postgres store: read source: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanSource(tx.QueryRow(ctx, `
SELECT s.id,s.tenant_id,s.vendor_id,v.slug,v.name,s.requested_url,COALESCE(s.final_url,''),s.canonical_url,
       s.source_type,COALESCE(s.adapter_name,''),COALESCE(s.adapter_version,''),s.enabled,s.health_state,
       s.failure_streak,s.last_attempt_at,s.last_success_at,s.next_poll_at,s.updated_at
FROM sources s JOIN vendors v ON v.id=s.vendor_id
WHERE s.deleted_at IS NULL AND s.id=$2 AND (s.tenant_id IS NULL OR s.tenant_id=$1)`, tenantID, sourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceView{}, ErrNotFound
	}
	if err != nil {
		return SourceView{}, err
	}
	collections, err := readCollections(ctx, tx, tenantID, []string{sourceID})
	if err != nil {
		return SourceView{}, err
	}
	item.Collection = collections[item.ID].source.Collection
	if err := tx.Commit(ctx); err != nil {
		return SourceView{}, fmt.Errorf("postgres store: read source: commit: %w", err)
	}
	return item, nil
}

type controlPlaneRowScanner interface{ Scan(...any) error }

func scanSource(row controlPlaneRowScanner) (SourceView, error) {
	var item SourceView
	if err := row.Scan(&item.ID, &item.TenantID, &item.VendorID, &item.VendorSlug, &item.VendorName,
		&item.RequestedURL, &item.FinalURL, &item.CanonicalURL, &item.SourceType, &item.AdapterName,
		&item.AdapterVersion, &item.Enabled, &item.HealthState, &item.FailureStreak,
		&item.LastAttemptAt, &item.LastSuccessAt, &item.NextPollAt, &item.UpdatedAt); err != nil {
		return SourceView{}, fmt.Errorf("postgres store: scan source: %w", err)
	}
	return item, nil
}

func (s *Store) ListIncidents(ctx context.Context, tenantID, vendorID, phase string, since *time.Time, cursor *TimeCursor, limit int) ([]IncidentView, error) {
	if err := validateList(tenantID, cursor, limit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list incidents: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorTime, cursorID := cursorValues(cursor)
	rows, err := tx.Query(ctx, `
SELECT i.id,i.source_id,v.id,v.slug,v.name,i.upstream_id,i.name,i.canonical_phase,i.raw_phase,
       i.canonical_impact,COALESCE(i.raw_impact,''),i.started_at,i.resolved_at,i.source_updated_at,
       i.observed_at,i.updated_at
FROM incidents i JOIN sources s ON s.id=i.source_id JOIN vendors v ON v.id=s.vendor_id
WHERE (s.tenant_id IS NULL OR s.tenant_id=$1)
  AND ($2='' OR v.id::text=$2 OR v.slug=$2)
  AND ($3='' OR i.canonical_phase=$3)
  AND ($4::timestamptz IS NULL OR i.updated_at >= $4)
  AND ($5::timestamptz IS NULL OR (i.updated_at,i.id)<($5,$6::uuid))
ORDER BY i.updated_at DESC,i.id DESC LIMIT $7`, tenantID, vendorID, phase, since, cursorTime, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list incidents: %w", err)
	}
	defer rows.Close()
	result := make([]IncidentView, 0, limit)
	for rows.Next() {
		item, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan incidents: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list incidents: commit: %w", err)
	}
	return result, nil
}

func (s *Store) Incident(ctx context.Context, tenantID, incidentID string) (IncidentView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return IncidentView{}, invalid("incident tenant ID must be a UUID")
	}
	if _, err := uuid.Parse(incidentID); err != nil {
		return IncidentView{}, invalid("incident ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return IncidentView{}, fmt.Errorf("postgres store: read incident: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanIncident(tx.QueryRow(ctx, `
SELECT i.id,i.source_id,v.id,v.slug,v.name,i.upstream_id,i.name,i.canonical_phase,i.raw_phase,
       i.canonical_impact,COALESCE(i.raw_impact,''),i.started_at,i.resolved_at,i.source_updated_at,
       i.observed_at,i.updated_at
FROM incidents i JOIN sources s ON s.id=i.source_id JOIN vendors v ON v.id=s.vendor_id
WHERE i.id=$2 AND (s.tenant_id IS NULL OR s.tenant_id=$1)`, tenantID, incidentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return IncidentView{}, ErrNotFound
	}
	if err != nil {
		return IncidentView{}, err
	}
	rows, err := tx.Query(ctx, `
SELECT id,canonical_phase,raw_phase,body,source_updated_at,observed_at
FROM incident_updates WHERE incident_id=$1 ORDER BY observed_at,id`, incidentID)
	if err != nil {
		return IncidentView{}, fmt.Errorf("postgres store: list incident updates: %w", err)
	}
	defer rows.Close()
	item.Updates = make([]IncidentUpdateView, 0)
	for rows.Next() {
		var update IncidentUpdateView
		if err := rows.Scan(&update.ID, &update.Phase, &update.RawPhase, &update.Body,
			&update.SourceUpdatedAt, &update.ObservedAt); err != nil {
			return IncidentView{}, fmt.Errorf("postgres store: scan incident update: %w", err)
		}
		item.Updates = append(item.Updates, update)
	}
	if err := rows.Err(); err != nil {
		return IncidentView{}, fmt.Errorf("postgres store: scan incident updates: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return IncidentView{}, fmt.Errorf("postgres store: read incident: commit: %w", err)
	}
	return item, nil
}

func scanIncident(row controlPlaneRowScanner) (IncidentView, error) {
	var item IncidentView
	if err := row.Scan(&item.ID, &item.SourceID, &item.VendorID, &item.VendorSlug, &item.VendorName,
		&item.UpstreamID, &item.Name, &item.Phase, &item.RawPhase, &item.Impact, &item.RawImpact,
		&item.StartedAt, &item.ResolvedAt, &item.SourceUpdatedAt, &item.ObservedAt, &item.UpdatedAt); err != nil {
		return IncidentView{}, fmt.Errorf("postgres store: scan incident: %w", err)
	}
	return item, nil
}

func (s *Store) ListSubscriptions(ctx context.Context, tenantID string, cursor *TimeCursor, limit int) ([]SubscriptionView, error) {
	if err := validateList(tenantID, cursor, limit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list subscriptions: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorTime, cursorID := cursorValues(cursor)
	rows, err := tx.Query(ctx, `
SELECT id,tenant_id,name,enabled,rule_version,rule,created_at,updated_at
FROM subscriptions WHERE deleted_at IS NULL AND tenant_id=$1
  AND ($2::timestamptz IS NULL OR (updated_at,id)<($2,$3::uuid))
ORDER BY updated_at DESC,id DESC LIMIT $4`, tenantID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list subscriptions: %w", err)
	}
	defer rows.Close()
	result := make([]SubscriptionView, 0, limit)
	for rows.Next() {
		var item SubscriptionView
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Name, &item.Enabled, &item.RuleVersion,
			&item.Rule, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres store: scan subscription: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan subscriptions: %w", err)
	}
	for index := range result {
		if err := loadSubscriptionRelations(ctx, tx, &result[index]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list subscriptions: commit: %w", err)
	}
	return result, nil
}

func (s *Store) Subscription(ctx context.Context, tenantID, subscriptionID string) (SubscriptionView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return SubscriptionView{}, invalid("subscription tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: read subscription: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item SubscriptionView
	err = tx.QueryRow(ctx, `
SELECT id,tenant_id,name,enabled,rule_version,rule,created_at,updated_at
FROM subscriptions WHERE deleted_at IS NULL AND tenant_id=$1 AND id=$2`, tenantID, subscriptionID).Scan(
		&item.ID, &item.TenantID, &item.Name, &item.Enabled, &item.RuleVersion,
		&item.Rule, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubscriptionView{}, ErrNotFound
	}
	if err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: read subscription: %w", err)
	}
	if err := loadSubscriptionRelations(ctx, tx, &item); err != nil {
		return SubscriptionView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubscriptionView{}, fmt.Errorf("postgres store: read subscription: commit: %w", err)
	}
	return item, nil
}

func loadSubscriptionRelations(ctx context.Context, tx pgx.Tx, item *SubscriptionView) error {
	rows, err := tx.Query(ctx, `
SELECT COALESCE(vendor_id::text,''),COALESCE(component_id::text,''),COALESCE(component_key,''),COALESCE(tag,''),COALESCE(event_kind,'')
FROM subscription_scopes WHERE subscription_id=$1 ORDER BY id`, item.ID)
	if err != nil {
		return fmt.Errorf("postgres store: list subscription scopes: %w", err)
	}
	item.Scopes = make([]SubscriptionScopeInput, 0)
	for rows.Next() {
		var scope SubscriptionScopeInput
		if err := rows.Scan(&scope.VendorID, &scope.ComponentID, &scope.ComponentKey, &scope.Tag, &scope.EventKind); err != nil {
			rows.Close()
			return fmt.Errorf("postgres store: scan subscription scope: %w", err)
		}
		item.Scopes = append(item.Scopes, scope)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("postgres store: scan subscription scopes: %w", err)
	}
	rows.Close()
	endpointRows, err := tx.Query(ctx, `SELECT endpoint_id FROM subscription_endpoints WHERE subscription_id=$1 ORDER BY endpoint_id`, item.ID)
	if err != nil {
		return fmt.Errorf("postgres store: list subscription endpoints: %w", err)
	}
	defer endpointRows.Close()
	item.EndpointIDs = make([]string, 0)
	for endpointRows.Next() {
		var endpointID string
		if err := endpointRows.Scan(&endpointID); err != nil {
			return fmt.Errorf("postgres store: scan subscription endpoint: %w", err)
		}
		item.EndpointIDs = append(item.EndpointIDs, endpointID)
	}
	return endpointRows.Err()
}

func (s *Store) ListEndpoints(ctx context.Context, tenantID string, cursor *TimeCursor, limit int) ([]EndpointView, error) {
	if err := validateList(tenantID, cursor, limit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list endpoints: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorTime, cursorID := cursorValues(cursor)
	rows, err := tx.Query(ctx, `
SELECT id,tenant_id,channel,name,enabled,key_id,secret_version,health_state,rate_limit_config,created_at,updated_at
FROM endpoints WHERE deleted_at IS NULL AND tenant_id=$1
  AND ($2::timestamptz IS NULL OR (updated_at,id)<($2,$3::uuid))
ORDER BY updated_at DESC,id DESC LIMIT $4`, tenantID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list endpoints: %w", err)
	}
	defer rows.Close()
	result := make([]EndpointView, 0, limit)
	for rows.Next() {
		item, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item.EndpointView)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan endpoints: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list endpoints: commit: %w", err)
	}
	return result, nil
}

func (s *Store) Endpoint(ctx context.Context, tenantID, endpointID string, includeSecret bool) (EndpointSecret, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return EndpointSecret{}, invalid("endpoint tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return EndpointSecret{}, fmt.Errorf("postgres store: read endpoint: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	columns := `id,tenant_id,channel,name,enabled,key_id,secret_version,health_state,rate_limit_config,created_at,updated_at`
	if includeSecret {
		columns += `,encrypted_config`
	}
	row := tx.QueryRow(ctx, `SELECT `+columns+` FROM endpoints WHERE deleted_at IS NULL AND tenant_id=$1 AND id=$2`, tenantID, endpointID)
	var item EndpointSecret
	if includeSecret {
		err = row.Scan(&item.ID, &item.TenantID, &item.Channel, &item.Name, &item.Enabled, &item.KeyID,
			&item.SecretVersion, &item.HealthState, &item.RateLimits, &item.CreatedAt, &item.UpdatedAt, &item.EncryptedConfig)
	} else {
		var scanned EndpointSecret
		err = row.Scan(&scanned.ID, &scanned.TenantID, &scanned.Channel, &scanned.Name, &scanned.Enabled,
			&scanned.KeyID, &scanned.SecretVersion, &scanned.HealthState, &scanned.RateLimits,
			&scanned.CreatedAt, &scanned.UpdatedAt)
		item = scanned
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return EndpointSecret{}, ErrNotFound
	}
	if err != nil {
		return EndpointSecret{}, fmt.Errorf("postgres store: scan endpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointSecret{}, fmt.Errorf("postgres store: read endpoint: commit: %w", err)
	}
	return item, nil
}

func scanEndpoint(row controlPlaneRowScanner) (EndpointSecret, error) {
	var item EndpointSecret
	if err := row.Scan(&item.ID, &item.TenantID, &item.Channel, &item.Name, &item.Enabled, &item.KeyID,
		&item.SecretVersion, &item.HealthState, &item.RateLimits, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return EndpointSecret{}, fmt.Errorf("postgres store: scan endpoint: %w", err)
	}
	return item, nil
}

func (s *Store) ListDeliveries(ctx context.Context, tenantID, status string, cursor *TimeCursor, limit int) ([]DeliveryView, error) {
	if err := validateList(tenantID, cursor, limit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list deliveries: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorTime, cursorID := cursorValues(cursor)
	rows, err := tx.Query(ctx, `
SELECT d.id,d.event_id,d.subscription_id,s.name,d.endpoint_id,e.name,e.channel,ce.event_kind,d.status,
       d.priority,d.attempt_count,COALESCE(d.provider_message_id,''),d.next_attempt_at,d.first_attempt_at,
       d.accepted_at,d.delivered_at,COALESCE(d.last_error_class,''),COALESCE(d.last_error_summary,''),d.created_at,d.updated_at
FROM deliveries d
JOIN subscriptions s ON s.id=d.subscription_id
JOIN endpoints e ON e.id=d.endpoint_id
JOIN canonical_events ce ON ce.id=d.event_id
WHERE s.tenant_id=$1 AND e.tenant_id=$1 AND ($2='' OR d.status=$2)
  AND ($3::timestamptz IS NULL OR (d.created_at,d.id)<($3,$4::uuid))
ORDER BY d.created_at DESC,d.id DESC LIMIT $5`, tenantID, status, cursorTime, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list deliveries: %w", err)
	}
	defer rows.Close()
	result := make([]DeliveryView, 0, limit)
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan deliveries: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list deliveries: commit: %w", err)
	}
	return result, nil
}

func (s *Store) Delivery(ctx context.Context, tenantID, deliveryID string) (DeliveryView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return DeliveryView{}, invalid("delivery tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return DeliveryView{}, fmt.Errorf("postgres store: read delivery: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanDelivery(tx.QueryRow(ctx, `
SELECT d.id,d.event_id,d.subscription_id,s.name,d.endpoint_id,e.name,e.channel,ce.event_kind,d.status,
       d.priority,d.attempt_count,COALESCE(d.provider_message_id,''),d.next_attempt_at,d.first_attempt_at,
       d.accepted_at,d.delivered_at,COALESCE(d.last_error_class,''),COALESCE(d.last_error_summary,''),d.created_at,d.updated_at
FROM deliveries d JOIN subscriptions s ON s.id=d.subscription_id
JOIN endpoints e ON e.id=d.endpoint_id JOIN canonical_events ce ON ce.id=d.event_id
WHERE d.id=$2 AND s.tenant_id=$1 AND e.tenant_id=$1`, tenantID, deliveryID))
	if errors.Is(err, pgx.ErrNoRows) {
		return DeliveryView{}, ErrNotFound
	}
	if err != nil {
		return DeliveryView{}, err
	}
	rows, err := tx.Query(ctx, `
SELECT attempt_number,secret_version,status,started_at,finished_at,http_status,COALESCE(provider_code,''),
       COALESCE(provider_message_id,''),retry_after,COALESCE(error_class,''),COALESCE(error_summary,''),response_metadata
FROM delivery_attempts WHERE delivery_id=$1 ORDER BY attempt_number`, deliveryID)
	if err != nil {
		return DeliveryView{}, fmt.Errorf("postgres store: list delivery attempts: %w", err)
	}
	defer rows.Close()
	item.Attempts = make([]DeliveryAttemptView, 0)
	for rows.Next() {
		var attempt DeliveryAttemptView
		if err := rows.Scan(&attempt.AttemptNumber, &attempt.SecretVersion, &attempt.Status, &attempt.StartedAt,
			&attempt.FinishedAt, &attempt.HTTPStatus, &attempt.ProviderCode, &attempt.ProviderMessageID,
			&attempt.RetryAfter, &attempt.ErrorClass, &attempt.ErrorSummary, &attempt.ResponseMetadata); err != nil {
			return DeliveryView{}, fmt.Errorf("postgres store: scan delivery attempt: %w", err)
		}
		item.Attempts = append(item.Attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return DeliveryView{}, fmt.Errorf("postgres store: scan delivery attempts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DeliveryView{}, fmt.Errorf("postgres store: read delivery: commit: %w", err)
	}
	return item, nil
}

func scanDelivery(row controlPlaneRowScanner) (DeliveryView, error) {
	var item DeliveryView
	if err := row.Scan(&item.ID, &item.EventID, &item.SubscriptionID, &item.SubscriptionName,
		&item.EndpointID, &item.EndpointName, &item.Channel, &item.EventKind, &item.Status,
		&item.Priority, &item.AttemptCount, &item.ProviderMessageID, &item.NextAttemptAt,
		&item.FirstAttemptAt, &item.AcceptedAt, &item.DeliveredAt, &item.LastErrorClass,
		&item.LastErrorSummary, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return DeliveryView{}, fmt.Errorf("postgres store: scan delivery: %w", err)
	}
	return item, nil
}

func (s *Store) ListTenantEvents(ctx context.Context, tenantID string, cursor *TimeCursor, limit int) ([]EventView, error) {
	if err := validateList(tenantID, cursor, limit); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres store: list tenant events: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorTime, cursorID := cursorValues(cursor)
	rows, err := tx.Query(ctx, `
SELECT ce.id,ce.source_id,v.id,v.slug,ce.event_kind,ce.entity_type,COALESCE(ce.entity_id,''),
       ce.aggregate_revision,ce.canonical_schema_version,ce.canonical_payload,ce.source_updated_at,
       ce.observed_at,ce.ingested_at
FROM canonical_events ce JOIN sources s ON s.id=ce.source_id JOIN vendors v ON v.id=s.vendor_id
WHERE (s.tenant_id IS NULL OR s.tenant_id=$1)
  AND ($2::timestamptz IS NULL OR (ce.ingested_at,ce.id)>($2,$3::uuid))
ORDER BY ce.ingested_at,ce.id LIMIT $4`, tenantID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list tenant events: %w", err)
	}
	defer rows.Close()
	result := make([]EventView, 0, limit)
	for rows.Next() {
		item, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan tenant events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: list tenant events: commit: %w", err)
	}
	return result, nil
}

func (s *Store) TenantEvent(ctx context.Context, tenantID, eventID string) (EventView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return EventView{}, invalid("event tenant ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return EventView{}, fmt.Errorf("postgres store: read tenant event: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanEvent(tx.QueryRow(ctx, `
SELECT ce.id,ce.source_id,v.id,v.slug,ce.event_kind,ce.entity_type,COALESCE(ce.entity_id,''),
       ce.aggregate_revision,ce.canonical_schema_version,ce.canonical_payload,ce.source_updated_at,
       ce.observed_at,ce.ingested_at
FROM canonical_events ce JOIN sources s ON s.id=ce.source_id JOIN vendors v ON v.id=s.vendor_id
WHERE ce.id=$2 AND (s.tenant_id IS NULL OR s.tenant_id=$1)`, tenantID, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EventView{}, ErrNotFound
	}
	if err != nil {
		return EventView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EventView{}, fmt.Errorf("postgres store: read tenant event: commit: %w", err)
	}
	return item, nil
}

func scanEvent(row controlPlaneRowScanner) (EventView, error) {
	var item EventView
	if err := row.Scan(&item.ID, &item.SourceID, &item.VendorID, &item.VendorSlug, &item.Kind,
		&item.EntityKind, &item.EntityID, &item.AggregateRevision, &item.SchemaVersion, &item.Payload,
		&item.SourceUpdatedAt, &item.ObservedAt, &item.IngestedAt); err != nil {
		return EventView{}, fmt.Errorf("postgres store: scan event: %w", err)
	}
	return item, nil
}

func validateList(tenantID string, cursor *TimeCursor, limit int) error {
	if _, err := uuid.Parse(tenantID); err != nil {
		return invalid("list tenant ID must be a UUID")
	}
	if limit <= 0 || limit > 500 {
		return invalid("list limit must be in [1,500]")
	}
	if cursor != nil {
		if cursor.Time.IsZero() {
			return invalid("cursor time is required")
		}
		if _, err := uuid.Parse(cursor.ID); err != nil {
			return invalid("cursor ID must be a UUID")
		}
	}
	return nil
}

func cursorValues(cursor *TimeCursor) (any, any) {
	if cursor == nil {
		return nil, nil
	}
	return cursor.Time, cursor.ID
}
