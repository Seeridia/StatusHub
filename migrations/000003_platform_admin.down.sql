BEGIN;

DROP TRIGGER IF EXISTS platform_audit_events_no_update ON platform_audit_events;
DROP TABLE IF EXISTS platform_audit_events;
DROP TABLE IF EXISTS platform_admins;
ALTER TABLE users DROP COLUMN IF EXISTS enabled;

COMMIT;
