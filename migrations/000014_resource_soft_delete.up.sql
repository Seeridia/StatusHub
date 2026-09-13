ALTER TABLE sources ADD COLUMN deleted_at timestamptz,
 ADD CONSTRAINT sources_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled);
ALTER TABLE subscriptions ADD COLUMN deleted_at timestamptz,
 ADD CONSTRAINT subscriptions_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled);
ALTER TABLE endpoints ADD COLUMN deleted_at timestamptz,
 ADD CONSTRAINT endpoints_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled);
DROP INDEX sources_tenant_canonical_url_uq;
CREATE UNIQUE INDEX sources_tenant_canonical_url_uq ON sources(tenant_id,canonical_url)
 WHERE tenant_id IS NOT NULL AND deleted_at IS NULL;
