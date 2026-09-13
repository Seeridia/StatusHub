BEGIN;

-- Refuse rollback if a deleted URL has since been re-added: preserve all history.
DROP INDEX sources_tenant_canonical_url_uq;
CREATE UNIQUE INDEX sources_tenant_canonical_url_uq ON sources(tenant_id,canonical_url) WHERE tenant_id IS NOT NULL;
ALTER TABLE endpoints DROP CONSTRAINT endpoints_deleted_disabled, DROP COLUMN deleted_at;
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_deleted_disabled, DROP COLUMN deleted_at;
ALTER TABLE sources DROP CONSTRAINT sources_deleted_disabled, DROP COLUMN deleted_at;

COMMIT;
