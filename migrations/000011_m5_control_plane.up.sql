BEGIN;

CREATE TABLE api_idempotency_keys (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    method text NOT NULL,
    route text NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    state text NOT NULL DEFAULT 'processing' CHECK (state IN ('processing','completed')),
    resource_id uuid,
    response_status integer CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599),
    response_body jsonb,
    locked_until timestamptz NOT NULL DEFAULT (statement_timestamp() + interval '30 seconds'),
    expires_at timestamptz NOT NULL DEFAULT (statement_timestamp() + interval '24 hours'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, idempotency_key)
);

CREATE INDEX api_idempotency_expiry_idx ON api_idempotency_keys (expires_at);

CREATE TABLE endpoint_test_jobs (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    endpoint_id uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
    lease_owner text,
    lease_token uuid,
    lease_until timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    provider_message_id text,
    http_status integer,
    error_class text,
    error_summary text,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz
);

CREATE INDEX endpoint_test_jobs_ready_idx
    ON endpoint_test_jobs (created_at, id)
    WHERE status IN ('queued','running');

CREATE INDEX sources_api_tenant_idx ON sources (tenant_id, updated_at DESC, id);
CREATE INDEX incidents_api_timeline_idx ON incidents (updated_at DESC, id);
CREATE INDEX deliveries_api_subscription_idx ON deliveries (subscription_id, created_at DESC, id);
CREATE INDEX endpoints_api_tenant_idx ON endpoints (tenant_id, updated_at DESC, id);
CREATE INDEX subscriptions_api_tenant_idx ON subscriptions (tenant_id, updated_at DESC, id);

COMMIT;
