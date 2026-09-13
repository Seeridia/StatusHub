BEGIN;

ALTER TABLE outbox
    ADD COLUMN lease_owner text,
    ADD COLUMN lease_token uuid,
    ADD COLUMN lease_until timestamptz,
    ADD CONSTRAINT outbox_lease_fields_consistent CHECK (
        (lease_owner IS NULL AND lease_token IS NULL AND lease_until IS NULL)
        OR
        (lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_until IS NOT NULL)
    );

DROP INDEX outbox_unpublished_idx;

CREATE INDEX outbox_claimable_idx
    ON outbox (available_at, lease_until, id)
    WHERE published_at IS NULL;

COMMIT;
