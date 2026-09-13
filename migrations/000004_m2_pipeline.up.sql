BEGIN;

ALTER TABLE subscription_scopes
    ADD COLUMN component_key text;

CREATE INDEX subscription_scopes_component_key_idx
    ON subscription_scopes (component_key)
    WHERE component_key IS NOT NULL;

CREATE TABLE fanout_plan_shards (
    plan_id uuid NOT NULL REFERENCES fanout_plans(id) ON DELETE CASCADE,
    shard_number integer NOT NULL CHECK (shard_number >= 0),
    cursor_subscription_id uuid,
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'completed')),
    lease_owner text,
    lease_token uuid,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    matched_deliveries bigint NOT NULL DEFAULT 0 CHECK (matched_deliveries >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (plan_id, shard_number)
);

CREATE INDEX fanout_plan_shards_ready_idx
    ON fanout_plan_shards (status, lease_until, plan_id, shard_number)
    WHERE status <> 'completed';

ALTER TABLE deliveries
    ADD COLUMN matched_rule_version integer NOT NULL DEFAULT 1 CHECK (matched_rule_version > 0),
    ADD COLUMN eligible_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN first_attempt_at timestamptz,
    ADD COLUMN lease_owner text,
    ADD COLUMN lease_token uuid,
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN last_error_class text,
    ADD COLUMN last_error_summary text;

CREATE INDEX deliveries_lease_ready_idx
    ON deliveries (priority DESC, next_attempt_at, id)
    WHERE status IN ('queued', 'retry_wait', 'sending');

CREATE INDEX deliveries_provider_message_idx
    ON deliveries (endpoint_id, provider_message_id)
    WHERE provider_message_id IS NOT NULL;

COMMIT;
