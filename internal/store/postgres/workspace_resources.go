package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) FindSourceByCanonicalURL(ctx context.Context, tenantID, canonicalURL string) (SourceView, error) {
	if _, err := uuid.Parse(tenantID); err != nil || strings.TrimSpace(canonicalURL) == "" {
		return SourceView{}, invalid("source tenant or canonical URL is invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SourceView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanSource(tx.QueryRow(ctx, `
SELECT s.id,s.tenant_id,s.vendor_id,v.slug,v.name,s.requested_url,COALESCE(s.final_url,''),s.canonical_url,
 s.source_type,COALESCE(s.adapter_name,''),COALESCE(s.adapter_version,''),
 (COALESCE(ws.enabled,false) AND s.enabled),s.health_state,s.failure_streak,s.last_attempt_at,s.last_success_at,
 s.next_poll_at,GREATEST(s.updated_at,COALESCE(ws.updated_at,s.updated_at)),
 CASE WHEN s.tenant_id IS NULL THEN 'platform' ELSE 'workspace' END,
 COALESCE(ws.display_name,''),ws.archived_at,COALESCE(ws.archive_reason,'')
FROM sources s JOIN vendors v ON v.id=s.vendor_id
LEFT JOIN workspace_sources ws ON ws.source_id=s.id AND ws.tenant_id=$1
WHERE s.deleted_at IS NULL AND s.canonical_url=$2 AND (s.tenant_id IS NULL OR s.tenant_id=$1)
ORDER BY (s.tenant_id IS NULL) DESC LIMIT 1`, tenantID, canonicalURL))
	if err == pgx.ErrNoRows {
		return SourceView{}, ErrNotFound
	}
	if err != nil {
		return SourceView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SourceView{}, err
	}
	return item, nil
}

func (s *Store) ListSourceCatalog(ctx context.Context, tenantID, query string, limit int) ([]SourceCatalogItem, error) {
	if _, err := uuid.Parse(tenantID); err != nil || limit <= 0 || limit > 200 {
		return nil, invalid("source catalog arguments are invalid")
	}
	query = strings.TrimSpace(query)
	rows, err := s.db.Query(ctx, `
SELECT s.id,s.vendor_id,v.slug,v.name,s.canonical_url,s.health_state,
       (ws.source_id IS NOT NULL)
FROM sources s JOIN vendors v ON v.id=s.vendor_id
LEFT JOIN workspace_sources ws ON ws.tenant_id=$1 AND ws.source_id=s.id
WHERE s.tenant_id IS NULL AND s.deleted_at IS NULL
  AND ($2='' OR v.name ILIKE '%'||$2||'%' OR v.slug ILIKE '%'||$2||'%' OR s.canonical_url ILIKE '%'||$2||'%')
ORDER BY (ws.source_id IS NOT NULL) DESC,v.name,s.id LIMIT $3`, tenantID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres store: source catalog: %w", err)
	}
	defer rows.Close()
	items := make([]SourceCatalogItem, 0, limit)
	for rows.Next() {
		var item SourceCatalogItem
		if err := rows.Scan(&item.ID, &item.VendorID, &item.VendorSlug, &item.VendorName, &item.CanonicalURL, &item.HealthState, &item.Added); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AttachSource(ctx context.Context, tenantID, sourceID, displayName, replacesSourceID string, actor AuditActor) (SourceView, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return SourceView{}, invalid("source tenant ID is invalid")
	}
	if _, err := uuid.Parse(sourceID); err != nil {
		return SourceView{}, invalid("source ID is invalid")
	}
	displayName = strings.TrimSpace(displayName)
	if len([]rune(displayName)) > 120 {
		return SourceView{}, invalid("source display name is invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SourceView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
INSERT INTO workspace_sources(tenant_id,source_id,display_name,enabled)
SELECT $1,s.id,$3,true FROM sources s
WHERE s.id=$2 AND s.deleted_at IS NULL AND (s.tenant_id IS NULL OR s.tenant_id=$1)
ON CONFLICT (tenant_id,source_id) DO UPDATE SET
 display_name=CASE WHEN EXCLUDED.display_name='' THEN workspace_sources.display_name ELSE EXCLUDED.display_name END,
 enabled=true,archived_at=NULL,archive_reason='',replaced_by_source_id=NULL,updated_at=statement_timestamp()`, tenantID, sourceID, displayName)
	if err != nil {
		return SourceView{}, err
	}
	if tag.RowsAffected() != 1 {
		return SourceView{}, ErrNotFound
	}
	if replacesSourceID != "" && replacesSourceID != sourceID {
		if err := archiveWorkspaceSourceTx(ctx, tx, tenantID, replacesSourceID, "replaced", sourceID, actor); err != nil {
			return SourceView{}, err
		}
	}
	metadata, _ := json.Marshal(map[string]any{"display_name": displayName, "replaces_source_id": replacesSourceID})
	if _, err = appendAuditTx(ctx, tx, tenantID, auditInput(actor, "source.attach", "source", sourceID, metadata)); err != nil {
		return SourceView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SourceView{}, err
	}
	return s.Source(ctx, tenantID, sourceID)
}

func archiveWorkspaceSourceTx(ctx context.Context, tx pgx.Tx, tenantID, sourceID, reason, replacementID string, actor AuditActor) error {
	var vendorID string
	err := tx.QueryRow(ctx, `
UPDATE workspace_sources ws SET enabled=false,archived_at=COALESCE(archived_at,statement_timestamp()),
 archive_reason=$3,replaced_by_source_id=NULLIF($4,'')::uuid,updated_at=statement_timestamp()
FROM sources s WHERE ws.tenant_id=$1 AND ws.source_id=$2 AND s.id=ws.source_id
RETURNING s.vendor_id`, tenantID, sourceID, reason, replacementID).Scan(&vendorID)
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// A workspace-owned collector has no other consumer. Stop it once its
	// workspace relation is archived; shared collectors keep running for the
	// remaining workspaces.
	if _, err = tx.Exec(ctx, `
UPDATE sources s SET enabled=false,updated_at=statement_timestamp()
WHERE s.id=$2 AND s.tenant_id=$1
 AND NOT EXISTS (
   SELECT 1 FROM workspace_sources ws
   WHERE ws.source_id=s.id AND ws.archived_at IS NULL AND ws.enabled
 )`, tenantID, sourceID); err != nil {
		return err
	}
	// Explicit vendor-scoped rules pause only when the vendor has no remaining active source.
	if _, err = tx.Exec(ctx, `
UPDATE subscriptions sub SET enabled=false,pause_reason='source_archived',rule_version=rule_version+1,updated_at=statement_timestamp()
WHERE sub.tenant_id=$1 AND sub.enabled AND sub.deleted_at IS NULL
 AND EXISTS(SELECT 1 FROM subscription_scopes ss WHERE ss.subscription_id=sub.id AND ss.vendor_id=$2)
 AND NOT EXISTS(
   SELECT 1 FROM workspace_sources active_ws JOIN sources active_s ON active_s.id=active_ws.source_id
   WHERE active_ws.tenant_id=$1 AND active_ws.archived_at IS NULL AND active_ws.enabled AND active_s.vendor_id=$2
 )`, tenantID, vendorID); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]string{"reason": reason, "replaced_by_source_id": replacementID})
	_, err = appendAuditTx(ctx, tx, tenantID, auditInput(actor, "source.archive", "source", sourceID, metadata))
	return err
}

func (s *Store) ArchiveSource(ctx context.Context, tenantID, sourceID string, actor AuditActor) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = archiveWorkspaceSourceTx(ctx, tx, tenantID, sourceID, "user_archived", "", actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RestoreResource(ctx context.Context, tenantID, id, kind string, actor AuditActor) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tag pgconn.CommandTag
	switch kind {
	case "source":
		tag, err = tx.Exec(ctx, `UPDATE workspace_sources SET archived_at=NULL,archive_reason='',replaced_by_source_id=NULL,enabled=false,updated_at=statement_timestamp() WHERE tenant_id=$1 AND source_id=$2 AND archived_at IS NOT NULL`, tenantID, id)
	case "subscription":
		tag, err = tx.Exec(ctx, `UPDATE subscriptions SET deleted_at=NULL,enabled=false,pause_reason='',rule_version=rule_version+1,updated_at=statement_timestamp() WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NOT NULL`, tenantID, id)
	case "endpoint":
		tag, err = tx.Exec(ctx, `UPDATE endpoints SET deleted_at=NULL,enabled=false,updated_at=statement_timestamp() WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NOT NULL`, tenantID, id)
	default:
		return invalid("unsupported resource kind")
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = appendAuditTx(ctx, tx, tenantID, auditInput(actor, kind+".restore", kind, id, json.RawMessage(`{"enabled":false}`))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
