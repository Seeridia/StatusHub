BEGIN;

DROP INDEX IF EXISTS deliveries_provider_message_idx;
DROP INDEX IF EXISTS deliveries_lease_ready_idx;

ALTER TABLE deliveries
    DROP COLUMN IF EXISTS last_error_summary,
    DROP COLUMN IF EXISTS last_error_class,
    DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_owner,
    DROP COLUMN IF EXISTS first_attempt_at,
    DROP COLUMN IF EXISTS eligible_at,
    DROP COLUMN IF EXISTS matched_rule_version;

DROP TABLE IF EXISTS fanout_plan_shards;

DROP INDEX IF EXISTS subscription_scopes_component_key_idx;
ALTER TABLE subscription_scopes DROP COLUMN IF EXISTS component_key;

COMMIT;
