BEGIN;
ALTER TABLE sources DROP COLUMN last_failure_code;
COMMIT;
