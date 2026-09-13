BEGIN;

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

COMMIT;
