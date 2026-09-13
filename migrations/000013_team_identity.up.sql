BEGIN;
ALTER TABLE tenant_memberships ADD COLUMN enabled boolean NOT NULL DEFAULT true;
CREATE TABLE local_credentials (
 principal_id uuid PRIMARY KEY REFERENCES principals(id),
 email text NOT NULL UNIQUE,
 password_hash text NOT NULL,
 verified_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE browser_sessions (
 id text PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 principal_id uuid NOT NULL REFERENCES principals(id),
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX browser_sessions_principal ON browser_sessions(principal_id);
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
 purpose text NOT NULL CHECK(purpose IN ('verify','reset')),
 email text NOT NULL,
 invitation_id uuid REFERENCES team_invitations(id),
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE identity_mail_jobs (
 id uuid PRIMARY KEY,
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
COMMIT;
