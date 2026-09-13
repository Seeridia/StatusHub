BEGIN;

DROP INDEX IF EXISTS subscriptions_api_tenant_idx;
DROP INDEX IF EXISTS endpoints_api_tenant_idx;
DROP INDEX IF EXISTS deliveries_api_subscription_idx;
DROP INDEX IF EXISTS incidents_api_timeline_idx;
DROP INDEX IF EXISTS sources_api_tenant_idx;
DROP TABLE IF EXISTS endpoint_test_jobs;
DROP TABLE IF EXISTS api_idempotency_keys;

COMMIT;
