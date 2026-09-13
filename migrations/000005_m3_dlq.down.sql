BEGIN;

DROP INDEX IF EXISTS deliveries_dlq_idx;

ALTER TABLE deliveries
    DROP COLUMN IF EXISTS replay_count,
    DROP COLUMN IF EXISTS dead_letter_reason,
    DROP COLUMN IF EXISTS dead_lettered_at;

COMMIT;
