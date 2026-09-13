BEGIN;

DROP INDEX IF EXISTS outbox_claimable_idx;

ALTER TABLE outbox
    DROP CONSTRAINT IF EXISTS outbox_lease_fields_consistent,
    DROP COLUMN IF EXISTS lease_owner,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_until;

CREATE INDEX outbox_unpublished_idx
    ON outbox (available_at, id)
    WHERE published_at IS NULL;

COMMIT;
