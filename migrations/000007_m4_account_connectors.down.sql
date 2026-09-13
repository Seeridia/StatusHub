BEGIN;

DROP TABLE IF EXISTS connector_entity_states;
DROP TABLE IF EXISTS account_connectors;

ALTER TABLE sources
    DROP CONSTRAINT IF EXISTS sources_id_tenant_uq;

COMMIT;
