BEGIN;

DROP INDEX IF EXISTS deliveries_retry_ready_idx;
DROP INDEX IF EXISTS deliveries_bulk_ready_idx;
DROP INDEX IF EXISTS deliveries_critical_ready_idx;

ALTER TABLE deliveries
    DROP COLUMN IF EXISTS queue_class;

CREATE INDEX deliveries_lease_ready_idx
    ON deliveries (priority DESC, next_attempt_at, id)
    WHERE status IN ('queued', 'retry_wait', 'sending');

COMMIT;
