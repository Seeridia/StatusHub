BEGIN;

DROP TRIGGER IF EXISTS audit_events_no_update ON audit_events;
DROP FUNCTION IF EXISTS statushub_forbid_audit_mutation();
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS audit_heads;
DROP TABLE IF EXISTS service_accounts;
DROP TABLE IF EXISTS tenant_memberships;
DROP TABLE IF EXISTS principals;
DROP TABLE IF EXISTS oidc_providers;

COMMIT;
