BEGIN;

ALTER TABLE deliveries
    ADD COLUMN queue_class text GENERATED ALWAYS AS (
        CASE WHEN priority >= 100 THEN 'critical' ELSE 'bulk' END
    ) STORED;

DROP INDEX IF EXISTS deliveries_lease_ready_idx;

CREATE INDEX deliveries_critical_ready_idx
    ON deliveries (next_attempt_at, priority DESC, id)
    WHERE queue_class = 'critical' AND status = 'queued';

CREATE INDEX deliveries_bulk_ready_idx
    ON deliveries (next_attempt_at, priority DESC, id)
    WHERE queue_class = 'bulk' AND status = 'queued';

CREATE INDEX deliveries_retry_ready_idx
    ON deliveries (next_attempt_at, priority DESC, id)
    WHERE status IN ('retry_wait', 'sending');

COMMIT;
