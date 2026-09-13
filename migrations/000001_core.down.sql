BEGIN;

DROP TABLE IF EXISTS provider_callbacks;
DROP TABLE IF EXISTS delivery_attempts;
DROP TABLE IF EXISTS deliveries;
DROP TABLE IF EXISTS fanout_plans;
DROP TABLE IF EXISTS subscription_endpoints;
DROP TABLE IF EXISTS endpoints;
DROP TABLE IF EXISTS subscription_scopes;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS canonical_events;
DROP TABLE IF EXISTS incident_updates;
DROP TABLE IF EXISTS incidents;
DROP TABLE IF EXISTS components;
DROP TABLE IF EXISTS raw_objects;
DROP TABLE IF EXISTS source_capabilities;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS vendors;
DROP TABLE IF EXISTS tenants;

COMMIT;

