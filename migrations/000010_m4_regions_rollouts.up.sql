BEGIN;

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

COMMIT;
