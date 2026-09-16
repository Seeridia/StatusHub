BEGIN;

CREATE TABLE workspace_sources (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    display_name text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    archived_at timestamptz,
    archive_reason text NOT NULL DEFAULT '',
    replaced_by_source_id uuid REFERENCES sources(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, source_id),
    CHECK (char_length(display_name) <= 120),
    CHECK (archived_at IS NULL OR NOT enabled)
);

CREATE INDEX workspace_sources_active_idx
    ON workspace_sources (tenant_id, updated_at DESC, source_id)
    WHERE archived_at IS NULL;
CREATE INDEX workspace_sources_source_idx
    ON workspace_sources (source_id, tenant_id)
    WHERE archived_at IS NULL AND enabled;

-- Preserve the visibility of every source that an existing workspace could see.
INSERT INTO workspace_sources (tenant_id, source_id, display_name, enabled)
SELECT t.id, s.id, '', s.enabled
FROM tenants t
JOIN sources s ON s.tenant_id IS NULL OR s.tenant_id=t.id
WHERE s.deleted_at IS NULL
ON CONFLICT DO NOTHING;

ALTER TABLE subscriptions ADD COLUMN pause_reason text NOT NULL DEFAULT '';

-- Collapse human roles to the three workspace roles. Refuse to invent an
-- administrator for a workspace that has no active human member.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM tenants t
    WHERE NOT EXISTS (
      SELECT 1 FROM memberships m WHERE m.tenant_id=t.id AND m.enabled
    )
  ) THEN
    RAISE EXCEPTION 'workspace without an active human member; assign one before migration';
  END IF;
END $$;

CREATE TEMP TABLE selected_admins ON COMMIT DROP AS
SELECT tenant_id, user_id
FROM (
  SELECT m.tenant_id, m.user_id,
         row_number() OVER (
           PARTITION BY m.tenant_id
           ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
                    m.created_at, m.user_id
         ) AS position
  FROM memberships m
  WHERE m.enabled
) ranked
WHERE position=1;

UPDATE memberships SET role='operator', updated_at=statement_timestamp()
WHERE role IN ('owner','admin');
UPDATE memberships m SET role='admin', updated_at=statement_timestamp()
FROM selected_admins a
WHERE m.tenant_id=a.tenant_id AND m.user_id=a.user_id;

UPDATE service_accounts SET role='operator', updated_at=statement_timestamp()
WHERE role IN ('owner','admin');
UPDATE team_invitations SET revoked_at=COALESCE(revoked_at,statement_timestamp())
WHERE role IN ('owner','admin') AND accepted_at IS NULL;
UPDATE team_invitations SET role='operator' WHERE role IN ('owner','admin');

ALTER TABLE memberships DROP CONSTRAINT IF EXISTS memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
  CHECK (role IN ('viewer','operator','admin'));
ALTER TABLE team_invitations DROP CONSTRAINT IF EXISTS team_invitations_role_check;
ALTER TABLE team_invitations ADD CONSTRAINT team_invitations_role_check
  CHECK (role IN ('viewer','operator'));
ALTER TABLE service_accounts DROP CONSTRAINT IF EXISTS service_accounts_role_check;
ALTER TABLE service_accounts ADD CONSTRAINT service_accounts_role_check
  CHECK (role IN ('viewer','operator'));

CREATE UNIQUE INDEX memberships_one_active_admin
  ON memberships (tenant_id)
  WHERE enabled AND role='admin';

CREATE FUNCTION statushub_require_one_active_admin() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  workspace_id uuid;
  admin_count integer;
BEGIN
  IF TG_TABLE_NAME = 'tenants' THEN
    workspace_id := COALESCE(NEW.id, OLD.id);
  ELSE
    workspace_id := COALESCE(NEW.tenant_id, OLD.tenant_id);
  END IF;
  IF EXISTS (SELECT 1 FROM tenants WHERE id=workspace_id) THEN
    SELECT count(*) INTO admin_count
    FROM memberships
    WHERE tenant_id=workspace_id AND enabled AND role='admin';
    IF admin_count <> 1 THEN
      RAISE EXCEPTION 'workspace % must have exactly one active human admin', workspace_id;
    END IF;
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER memberships_require_one_active_admin
AFTER INSERT OR UPDATE OR DELETE ON memberships
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION statushub_require_one_active_admin();

CREATE CONSTRAINT TRIGGER tenants_require_one_active_admin
AFTER INSERT OR UPDATE ON tenants
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION statushub_require_one_active_admin();

COMMIT;
