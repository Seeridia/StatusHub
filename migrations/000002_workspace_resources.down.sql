BEGIN;
DROP TRIGGER IF EXISTS tenants_require_one_active_admin ON tenants;
DROP TRIGGER IF EXISTS memberships_require_one_active_admin ON memberships;
DROP FUNCTION IF EXISTS statushub_require_one_active_admin();
DROP INDEX IF EXISTS memberships_one_active_admin;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS pause_reason;
DROP TABLE IF EXISTS workspace_sources;
COMMIT;
