BEGIN;

CREATE TABLE oidc_providers (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    issuer text NOT NULL,
    client_id text NOT NULL,
    jwks_uri text,
    allowed_domains text[] NOT NULL DEFAULT '{}',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, issuer)
);

CREATE TABLE principals (
    id uuid PRIMARY KEY,
    issuer text NOT NULL,
    subject text NOT NULL,
    email text,
    display_name text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);

CREATE TABLE tenant_memberships (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    principal_id uuid NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('viewer','operator','admin','owner')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, principal_id)
);

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

COMMIT;
