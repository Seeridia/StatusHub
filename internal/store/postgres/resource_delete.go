package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DeleteResource disables and hides configuration without deleting historical foreign keys.
func (s *Store) DeleteResource(ctx context.Context, tenantID, id, kind string, actor AuditActor) error {
	if err := s.ready(); err != nil {
		return err
	}
	table := map[string]string{"source": "sources", "subscription": "subscriptions", "endpoint": "endpoints"}[kind]
	if table == "" {
		return invalid("unsupported resource kind")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE `+table+` SET enabled=false,deleted_at=COALESCE(deleted_at,statement_timestamp()),updated_at=statement_timestamp() WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("soft delete %s: %w", kind, err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if kind == "endpoint" {
		// Serialize against rule edits, then remove references atomically.
		rows, err := tx.Query(ctx, `SELECT s.id FROM subscriptions s JOIN subscription_endpoints se ON se.subscription_id=s.id WHERE s.tenant_id=$1 AND se.endpoint_id=$2 ORDER BY s.id FOR UPDATE OF s`, tenantID, id)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var rule string
			if err := rows.Scan(&rule); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, rule)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM subscription_endpoints WHERE endpoint_id=$1`, id); err != nil {
			return err
		}
		for _, rule := range ids {
			if _, err = tx.Exec(ctx, `UPDATE subscriptions SET enabled=enabled AND EXISTS(SELECT 1 FROM subscription_endpoints WHERE subscription_id=$1),rule_version=rule_version+1,updated_at=statement_timestamp() WHERE id=$1`, rule); err != nil {
				return err
			}
			metadata, _ := json.Marshal(map[string]string{"removed_endpoint_id": id})
			if _, err = appendAuditTx(ctx, tx, tenantID, auditInput(actor, "subscription.endpoint_removed", "subscription", rule, metadata)); err != nil {
				return err
			}
		}
	}
	if _, err = appendAuditTx(ctx, tx, tenantID, auditInput(actor, kind+".delete", kind, id, json.RawMessage(`{"soft_deleted":true}`))); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
