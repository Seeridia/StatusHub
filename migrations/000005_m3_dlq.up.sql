BEGIN;

ALTER TABLE deliveries
    ADD COLUMN dead_lettered_at timestamptz,
    ADD COLUMN dead_letter_reason text,
    ADD COLUMN replay_count integer NOT NULL DEFAULT 0 CHECK (replay_count >= 0);

CREATE INDEX deliveries_dlq_idx
    ON deliveries (dead_lettered_at DESC, id DESC)
    WHERE status = 'dead_letter';

COMMIT;
