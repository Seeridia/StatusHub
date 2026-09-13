BEGIN;
ALTER TABLE sources ADD COLUMN last_failure_code text;
COMMIT;
