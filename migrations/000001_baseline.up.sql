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




ALTER TABLE canonical_events
    ALTER COLUMN entity_id TYPE text
    USING entity_id::text;




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




ALTER TABLE deliveries
    ADD COLUMN dead_lettered_at timestamptz,
    ADD COLUMN dead_letter_reason text,
    ADD COLUMN replay_count integer NOT NULL DEFAULT 0 CHECK (replay_count >= 0);

CREATE INDEX deliveries_dlq_idx
    ON deliveries (dead_lettered_at DESC, id DESC)
    WHERE status = 'dead_letter';




ALTER TABLE deliveries
    ADD COLUMN queue_class text GENERATED ALWAYS AS (
        CASE WHEN priority >= 100 THEN 'critical' ELSE 'bulk' END
    ) STORED;

DROP INDEX IF EXISTS deliveries_lease_ready_idx;

CREATE INDEX deliveries_critical_ready_idx
    ON deliveries (next_attempt_at, priority DESC, id)
    WHERE queue_class = 'critical' AND status = 'queued';

CREATE INDEX deliveries_bulk_ready_idx
    ON deliveries (next_attempt_at, priority DESC, id)
    WHERE queue_class = 'bulk' AND status = 'queued';

CREATE INDEX deliveries_retry_ready_idx
    ON deliveries (next_attempt_at, priority DESC, id)
    WHERE status IN ('retry_wait', 'sending');




ALTER TABLE sources
    ADD CONSTRAINT sources_id_tenant_uq UNIQUE (id, tenant_id);

CREATE TABLE account_connectors (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL UNIQUE,
    tenant_id uuid NOT NULL,
    provider text NOT NULL CHECK (provider = 'aws-account-health'),
    external_account_id text NOT NULL CHECK (external_account_id ~ '^[0-9]{12}$'),
    sns_topic_arn text NOT NULL,
    allowed_regions text[] NOT NULL DEFAULT '{}',
    allowed_services text[] NOT NULL DEFAULT '{}',
    enabled boolean NOT NULL DEFAULT true,
    last_event_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (source_id, tenant_id) REFERENCES sources(id, tenant_id) ON DELETE CASCADE,
    UNIQUE (tenant_id, provider, external_account_id, sns_topic_arn)
);

CREATE INDEX account_connectors_tenant_idx
    ON account_connectors (tenant_id, provider, enabled);

CREATE TABLE connector_entity_states (
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    entity_id text NOT NULL,
    entity_type text NOT NULL,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    latest_source_updated_at timestamptz,
    semantic_hash text,
    last_event_kind text,
    latest_payload jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, entity_id)
);




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


CREATE TABLE service_accounts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL,
    role text NOT NULL CHECK (role IN ('viewer','operator','admin','owner')),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    enabled boolean NOT NULL DEFAULT true,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE audit_heads (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    last_hash bytea NOT NULL DEFAULT decode(repeat('00',32),'hex') CHECK (octet_length(last_hash) = 32),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    occurred_at timestamptz NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    outcome text NOT NULL,
    request_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    previous_hash bytea NOT NULL CHECK (octet_length(previous_hash) = 32),
    event_hash bytea NOT NULL CHECK (octet_length(event_hash) = 32),
    PRIMARY KEY (tenant_id, sequence),
    UNIQUE (tenant_id, event_hash)
);

CREATE INDEX audit_events_export_idx
    ON audit_events (tenant_id, occurred_at, sequence);

CREATE FUNCTION statushub_forbid_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events are append-only';
END;
$$;

CREATE TRIGGER audit_events_no_update
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION statushub_forbid_audit_mutation();




CREATE TABLE regions (
    id text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT true,
    last_heartbeat_at timestamptz NOT NULL DEFAULT now(),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE source_ownership (
    source_id uuid PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    home_region text NOT NULL REFERENCES regions(id) ON DELETE RESTRICT,
    active_region text NOT NULL REFERENCES regions(id) ON DELETE RESTRICT,
    epoch bigint NOT NULL DEFAULT 1 CHECK (epoch > 0),
    state text NOT NULL DEFAULT 'home' CHECK (state IN ('home','failover')),
    changed_by text NOT NULL DEFAULT 'bootstrap',
    change_reason text NOT NULL DEFAULT 'initial ownership',
    promoted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX source_ownership_active_idx ON source_ownership (active_region, source_id);

CREATE TABLE adapter_rollouts (
    id uuid PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    baseline_adapter_name text NOT NULL,
    baseline_adapter_version text,
    candidate_adapter_name text NOT NULL,
    candidate_adapter_version text NOT NULL,
    state text NOT NULL DEFAULT 'shadow' CHECK (state IN ('shadow','promoted','rolled_back')),
    sample_rate double precision NOT NULL DEFAULT 1 CHECK (sample_rate > 0 AND sample_rate <= 1),
    minimum_samples integer NOT NULL DEFAULT 100 CHECK (minimum_samples > 0),
    maximum_mismatch_rate double precision NOT NULL DEFAULT 0 CHECK (maximum_mismatch_rate >= 0 AND maximum_mismatch_rate <= 1),
    maximum_error_rate double precision NOT NULL DEFAULT 0.01 CHECK (maximum_error_rate >= 0 AND maximum_error_rate <= 1),
    created_by text NOT NULL,
    decision_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, id)
);

CREATE UNIQUE INDEX adapter_rollouts_one_shadow_per_source_uq
    ON adapter_rollouts (source_id) WHERE state='shadow';

CREATE TABLE adapter_shadow_comparisons (
    id uuid PRIMARY KEY,
    rollout_id uuid NOT NULL REFERENCES adapter_rollouts(id) ON DELETE CASCADE,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    resource_kind text NOT NULL,
    observed_at timestamptz NOT NULL,
    primary_digest bytea,
    candidate_digest bytea,
    equivalent boolean NOT NULL,
    candidate_error text,
    primary_duration_ms bigint NOT NULL CHECK (primary_duration_ms >= 0),
    candidate_duration_ms bigint NOT NULL CHECK (candidate_duration_ms >= 0),
    difference jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (source_id, rollout_id) REFERENCES adapter_rollouts(source_id, id) ON DELETE CASCADE
);

CREATE INDEX adapter_shadow_rollout_stats_idx
    ON adapter_shadow_comparisons (rollout_id, observed_at DESC);




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



ALTER TABLE sources ADD COLUMN last_failure_code text;


CREATE TABLE users (
 id uuid PRIMARY KEY, email text NOT NULL UNIQUE,
 display_name text NOT NULL DEFAULT '', password_hash text NOT NULL,
 email_verified_at timestamptz, password_version uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE memberships (
 tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id), role text NOT NULL CHECK (role IN ('viewer','operator','admin','owner')),
 enabled boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,user_id)
);
CREATE TABLE browser_sessions (
 token_hash bytea PRIMARY KEY, user_id uuid NOT NULL REFERENCES users(id),
 csrf text NOT NULL, expires_at timestamptz NOT NULL, last_active_at timestamptz NOT NULL DEFAULT now(),
 revoked_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX browser_sessions_user ON browser_sessions(user_id);
CREATE TABLE setup_tokens (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 token_hash bytea NOT NULL, expires_at timestamptz NOT NULL
);

CREATE TABLE team_invitations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 email text NOT NULL,
 role text NOT NULL CHECK(role IN ('viewer','operator','admin','owner')),
 token_hash bytea UNIQUE NOT NULL,
 actor_type text NOT NULL,
 actor_id text NOT NULL,
 expires_at timestamptz NOT NULL,
 accepted_at timestamptz,
 revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX team_invitations_tenant ON team_invitations(tenant_id,created_at);
CREATE TABLE identity_tokens (
 token_hash bytea PRIMARY KEY,
 purpose text NOT NULL CHECK(purpose IN ('reset','verify-email')),
 email text NOT NULL,
 user_id uuid NOT NULL REFERENCES users(id),
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE identity_mail_jobs (
 id uuid PRIMARY KEY,
 invitation_id uuid REFERENCES team_invitations(id),
 payload text,
 expires_at timestamptz NOT NULL,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0,
 lease_until timestamptz,
 lease_token uuid,
 completed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE identity_rate_limits (
 key text PRIMARY KEY,
 window_start timestamptz NOT NULL,
 attempts integer NOT NULL
);
CREATE TABLE identity_mutations (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 key text NOT NULL,
 request_hash bytea NOT NULL,
 result jsonb,
 secret_result text,
 secret_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,key)
);



ALTER TABLE sources ADD COLUMN deleted_at timestamptz,
 ADD CONSTRAINT sources_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled);
ALTER TABLE subscriptions ADD COLUMN deleted_at timestamptz,
 ADD CONSTRAINT subscriptions_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled);
ALTER TABLE endpoints ADD COLUMN deleted_at timestamptz,
 ADD CONSTRAINT endpoints_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled);
DROP INDEX sources_tenant_canonical_url_uq;
CREATE UNIQUE INDEX sources_tenant_canonical_url_uq ON sources(tenant_id,canonical_url)
 WHERE tenant_id IS NOT NULL AND deleted_at IS NULL;


COMMIT;
