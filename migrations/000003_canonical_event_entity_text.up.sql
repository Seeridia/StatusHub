BEGIN;

ALTER TABLE canonical_events
    ALTER COLUMN entity_id TYPE text
    USING entity_id::text;

COMMIT;
