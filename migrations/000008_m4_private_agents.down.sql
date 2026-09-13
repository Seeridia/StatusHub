BEGIN;

DROP TABLE IF EXISTS private_agent_endpoints;
DROP TABLE IF EXISTS private_agents;

ALTER TABLE endpoints
    DROP CONSTRAINT IF EXISTS endpoints_id_tenant_uq;

COMMIT;
