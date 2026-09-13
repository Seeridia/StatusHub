BEGIN;

CREATE TABLE tenants (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE vendors (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    canonical_domain text,
    aliases text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sources (
    id uuid PRIMARY KEY,
    tenant_id uuid REFERENCES tenants(id) ON DELETE CASCADE,
    vendor_id uuid NOT NULL REFERENCES vendors(id) ON DELETE RESTRICT,
    requested_url text NOT NULL,
    final_url text,
    canonical_url text NOT NULL,
    source_type text NOT NULL,
    adapter_name text,
    adapter_version text,
    enabled boolean NOT NULL DEFAULT true,
    health_state text NOT NULL DEFAULT 'unknown',
    next_poll_at timestamptz,
    lease_owner text,
    lease_token uuid,
    lease_until timestamptz,
    failure_streak integer NOT NULL DEFAULT 0 CHECK (failure_streak >= 0),
    last_attempt_at timestamptz,
    last_success_at timestamptz,
    last_checkpoint text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sources_public_canonical_url_uq
    ON sources (canonical_url)
    WHERE tenant_id IS NULL;

CREATE UNIQUE INDEX sources_tenant_canonical_url_uq
    ON sources (tenant_id, canonical_url)
    WHERE tenant_id IS NOT NULL;

CREATE INDEX sources_poll_due_idx
    ON sources (next_poll_at, id)
    WHERE enabled;

CREATE TABLE source_capabilities (
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    resource_kind text NOT NULL,
    endpoint_url text NOT NULL,
    engine text NOT NULL,
    engine_version text,
    adapter_version text NOT NULL,
    confidence double precision NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    authoritative boolean NOT NULL DEFAULT false,
    completeness text NOT NULL DEFAULT 'unknown',
    supports_etag boolean NOT NULL DEFAULT false,
    supports_last_modified boolean NOT NULL DEFAULT false,
    supports_pagination boolean NOT NULL DEFAULT false,
    requires_auth boolean NOT NULL DEFAULT false,
    history_window interval,
    max_body_bytes bigint CHECK (max_body_bytes IS NULL OR max_body_bytes > 0),
    schema_hash text,
    discovered_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    last_successful_checkpoint text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (source_id, resource_kind, endpoint_url)
);

CREATE TABLE raw_objects (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    endpoint_url text NOT NULL,
    observed_at timestamptz NOT NULL,
    source_updated_at timestamptz,
    etag text,
    last_modified text,
    content_type text,
    content_encoding text,
    byte_size bigint NOT NULL CHECK (byte_size >= 0),
    sha256 bytea NOT NULL,
    object_key text NOT NULL UNIQUE,
    response_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, endpoint_url, sha256)
);

CREATE TABLE components (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    upstream_id text NOT NULL,
    name text NOT NULL,
    canonical_status text NOT NULL,
    raw_status text NOT NULL,
    position integer,
    group_upstream_id text,
    tags text[] NOT NULL DEFAULT '{}',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    source_updated_at timestamptz,
    observed_at timestamptz NOT NULL,
    raw_object_id uuid REFERENCES raw_objects(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, upstream_id)
);

CREATE INDEX components_source_tags_idx ON components USING gin (tags);

CREATE TABLE incidents (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    upstream_id text NOT NULL,
    name text NOT NULL,
    canonical_phase text NOT NULL,
    raw_phase text NOT NULL,
    canonical_impact text NOT NULL,
    raw_impact text,
    started_at timestamptz,
    resolved_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    source_updated_at timestamptz,
    observed_at timestamptz NOT NULL,
    raw_object_id uuid REFERENCES raw_objects(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, upstream_id)
);

CREATE INDEX incidents_source_phase_idx ON incidents (source_id, canonical_phase, updated_at DESC);

CREATE TABLE incident_updates (
    id uuid PRIMARY KEY,
    incident_id uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    upstream_id text,
    source_event_key text NOT NULL,
    canonical_phase text NOT NULL,
    raw_phase text NOT NULL,
    body text NOT NULL,
    source_updated_at timestamptz,
    observed_at timestamptz NOT NULL,
    raw_object_id uuid REFERENCES raw_objects(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (incident_id, source_event_key)
);

CREATE TABLE canonical_events (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    entity_type text NOT NULL,
    entity_id uuid,
    event_kind text NOT NULL,
    aggregate_revision bigint NOT NULL CHECK (aggregate_revision > 0),
    source_event_key text NOT NULL,
    normalizer_version text NOT NULL,
    canonical_schema_version text NOT NULL,
    canonical_payload jsonb NOT NULL,
    source_updated_at timestamptz,
    observed_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    raw_object_id uuid REFERENCES raw_objects(id) ON DELETE SET NULL,
    UNIQUE (source_id, source_event_key)
);

CREATE INDEX canonical_events_timeline_idx
    ON canonical_events (source_id, observed_at DESC, id);

CREATE TABLE outbox (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES canonical_events(id) ON DELETE CASCADE,
    subject text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, subject)
);

CREATE INDEX outbox_unpublished_idx
    ON outbox (available_at, id)
    WHERE published_at IS NULL;

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    rule_version integer NOT NULL DEFAULT 1 CHECK (rule_version > 0),
    rule jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE subscription_scopes (
    id uuid PRIMARY KEY,
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    vendor_id uuid REFERENCES vendors(id) ON DELETE CASCADE,
    component_id uuid REFERENCES components(id) ON DELETE CASCADE,
    tag text,
    event_kind text,
    UNIQUE NULLS NOT DISTINCT (subscription_id, vendor_id, component_id, tag, event_kind)
);

CREATE INDEX subscription_scopes_lookup_idx
    ON subscription_scopes (vendor_id, component_id, event_kind, tag);

CREATE TABLE endpoints (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel text NOT NULL,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    encrypted_config bytea NOT NULL,
    key_id text NOT NULL,
    secret_version integer NOT NULL DEFAULT 1 CHECK (secret_version > 0),
    health_state text NOT NULL DEFAULT 'unknown',
    rate_limit_config jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE subscription_endpoints (
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    endpoint_id uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    PRIMARY KEY (subscription_id, endpoint_id)
);

CREATE TABLE fanout_plans (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES canonical_events(id) ON DELETE CASCADE,
    rule_version integer NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    shard_count integer NOT NULL CHECK (shard_count > 0),
    completed_shards integer NOT NULL DEFAULT 0 CHECK (completed_shards >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (event_id, rule_version)
);

CREATE TABLE deliveries (
    id uuid PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES canonical_events(id) ON DELETE CASCADE,
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    endpoint_id uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    template_version integer NOT NULL CHECK (template_version > 0),
    status text NOT NULL DEFAULT 'queued',
    priority smallint NOT NULL DEFAULT 0,
    provider_message_id text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    accepted_at timestamptz,
    delivered_at timestamptz,
    superseded_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, subscription_id, endpoint_id, template_version)
);

CREATE INDEX deliveries_ready_idx
    ON deliveries (priority DESC, next_attempt_at, id)
    WHERE status IN ('queued', 'retry_wait');

CREATE INDEX deliveries_endpoint_order_idx
    ON deliveries (endpoint_id, created_at, id)
    WHERE status IN ('queued', 'sending', 'retry_wait');

CREATE TABLE delivery_attempts (
    id uuid PRIMARY KEY,
    delivery_id uuid NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    secret_version integer NOT NULL CHECK (secret_version > 0),
    status text NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    http_status integer,
    provider_code text,
    provider_message_id text,
    retry_after timestamptz,
    error_class text,
    error_summary text,
    response_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (delivery_id, attempt_number)
);

CREATE TABLE provider_callbacks (
    id uuid PRIMARY KEY,
    endpoint_id uuid NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    delivery_id uuid REFERENCES deliveries(id) ON DELETE SET NULL,
    provider_event_id text NOT NULL,
    provider_message_id text,
    received_at timestamptz NOT NULL DEFAULT now(),
    raw_body_sha256 bytea NOT NULL,
    raw_object_key text,
    payload jsonb NOT NULL,
    UNIQUE (endpoint_id, provider_event_id)
);

COMMIT;
