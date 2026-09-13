BEGIN;

ALTER TABLE endpoints
    ADD CONSTRAINT endpoints_id_tenant_uq UNIQUE (id, tenant_id);

CREATE TABLE private_agents (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    enabled boolean NOT NULL DEFAULT true,
    last_seen_at timestamptz,
    agent_version text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, tenant_id),
    UNIQUE (tenant_id, name)
);

CREATE TABLE private_agent_endpoints (
    agent_id uuid NOT NULL,
    endpoint_id uuid NOT NULL UNIQUE,
    tenant_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, endpoint_id),
    FOREIGN KEY (agent_id, tenant_id) REFERENCES private_agents(id, tenant_id) ON DELETE CASCADE,
    FOREIGN KEY (endpoint_id, tenant_id) REFERENCES endpoints(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX private_agent_endpoints_agent_idx
    ON private_agent_endpoints (agent_id, endpoint_id);

COMMIT;
