package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrLastPlatformAdmin     = errors.New("the last active platform administrator cannot be removed or disabled")
	ErrPlatformSelfDisable   = errors.New("a platform administrator cannot disable their own account")
	ErrPlatformSelfRevoke    = errors.New("a platform administrator cannot revoke their own platform access")
	ErrWorkspaceAdminDisable = errors.New("transfer workspace administration before disabling this user")
)

type PlatformSummary struct {
	Users           int `json:"users"`
	EnabledUsers    int `json:"enabled_users"`
	Workspaces      int `json:"workspaces"`
	PlatformSources int `json:"platform_sources"`
}

type PlatformUser struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Enabled        bool       `json:"enabled"`
	PlatformAdmin  bool       `json:"platform_admin"`
	WorkspaceCount int        `json:"workspace_count"`
	ActiveSessions int        `json:"active_sessions"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSessionAt  *time.Time `json:"last_session_at,omitempty"`
}

type PlatformWorkspace struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Name       string    `json:"name"`
	AdminEmail string    `json:"admin_email"`
	Members    int       `json:"members"`
	Sources    int       `json:"sources"`
	CreatedAt  time.Time `json:"created_at"`
}

type PlatformAuditEvent struct {
	ID           int64           `json:"id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	ActorUserID  *string         `json:"actor_user_id,omitempty"`
	ActorEmail   string          `json:"actor_email"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Metadata     json.RawMessage `json:"metadata"`
}

func (s *Store) IsPlatformAdmin(ctx context.Context, user string) (bool, error) {
	if _, err := uuid.Parse(user); err != nil {
		return false, invalid("invalid user ID")
	}
	var allowed bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM platform_admins p JOIN users u ON u.id=p.user_id
 WHERE p.user_id=$1 AND p.enabled AND u.enabled
)`, user).Scan(&allowed)
	return allowed, err
}

func (s *Store) GrantPlatformAdmin(ctx context.Context, email, grantedBy string) (PlatformUser, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return PlatformUser{}, err
	}
	grantedBy = strings.TrimSpace(grantedBy)
	if grantedBy == "" {
		return PlatformUser{}, invalid("grant actor is required")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlatformUser{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 AND enabled FOR UPDATE`, email).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return PlatformUser{}, ErrNotFound
	} else if err != nil {
		return PlatformUser{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO platform_admins(user_id,enabled,granted_by)
 VALUES($1,true,$2) ON CONFLICT(user_id) DO UPDATE SET enabled=true,granted_by=excluded.granted_by,updated_at=now()`, id, grantedBy)
	if err != nil {
		return PlatformUser{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO platform_audit_events(actor_user_id,action,resource_type,resource_id,metadata)
 VALUES(NULL,'platform_admin.grant','user',$1,$2)`, id, json.RawMessage(`{"source":"cli"}`))
	if err != nil {
		return PlatformUser{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlatformUser{}, err
	}
	users, err := s.ListPlatformUsers(ctx, email, 100)
	if err != nil {
		return PlatformUser{}, err
	}
	for _, user := range users {
		if user.ID == id {
			return user, nil
		}
	}
	return PlatformUser{}, ErrNotFound
}

func (s *Store) RevokePlatformAdmin(ctx context.Context, email, revokedBy string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901258)`); err != nil {
		return err
	}
	var id string
	if err = tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN platform_admins p ON p.user_id=u.id WHERE u.email=$1 AND p.enabled FOR UPDATE`, email).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM platform_admins p JOIN users u ON u.id=p.user_id WHERE p.enabled AND u.enabled`).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastPlatformAdmin
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_admins SET enabled=false,updated_at=now() WHERE user_id=$1`, id); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]string{"source": "cli", "actor": strings.TrimSpace(revokedBy)})
	_, err = tx.Exec(ctx, `INSERT INTO platform_audit_events(actor_user_id,action,resource_type,resource_id,metadata)
	 VALUES(NULL,'platform_admin.revoke','user',$1,$2)`, id, metadata)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PlatformOverview(ctx context.Context) (PlatformSummary, error) {
	var x PlatformSummary
	err := s.db.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM users),
 (SELECT count(*) FROM users WHERE enabled),
 (SELECT count(*) FROM tenants),
 (SELECT count(*) FROM sources WHERE tenant_id IS NULL AND deleted_at IS NULL)`).Scan(&x.Users, &x.EnabledUsers, &x.Workspaces, &x.PlatformSources)
	return x, err
}

func (s *Store) ListPlatformUsers(ctx context.Context, query string, limit int) ([]PlatformUser, error) {
	query = strings.TrimSpace(query)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `SELECT u.id,u.email,u.display_name,u.enabled,
 EXISTS(SELECT 1 FROM platform_admins p WHERE p.user_id=u.id AND p.enabled),
 (SELECT count(*) FROM memberships m WHERE m.user_id=u.id AND m.enabled),
 (SELECT count(*) FROM browser_sessions b WHERE b.user_id=u.id AND b.revoked_at IS NULL AND b.expires_at>now() AND b.last_active_at>now()-interval '24 hours'),
 u.created_at,(SELECT max(b.last_active_at) FROM browser_sessions b WHERE b.user_id=u.id)
 FROM users u WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR u.display_name ILIKE '%'||$1||'%')
 ORDER BY u.created_at DESC,u.id DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformUser{}
	for rows.Next() {
		var x PlatformUser
		if err = rows.Scan(&x.ID, &x.Email, &x.Name, &x.Enabled, &x.PlatformAdmin, &x.WorkspaceCount, &x.ActiveSessions, &x.CreatedAt, &x.LastSessionAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) ListPlatformWorkspaces(ctx context.Context, query string, limit int) ([]PlatformWorkspace, error) {
	query = strings.TrimSpace(query)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `SELECT t.id,t.slug,t.name,COALESCE(a.email,''),
 (SELECT count(*) FROM memberships m WHERE m.tenant_id=t.id AND m.enabled),
 (SELECT count(*) FROM workspace_sources ws WHERE ws.tenant_id=t.id AND ws.archived_at IS NULL),t.created_at
 FROM tenants t LEFT JOIN memberships am ON am.tenant_id=t.id AND am.enabled AND am.role='admin'
 LEFT JOIN users a ON a.id=am.user_id
 WHERE ($1='' OR t.slug ILIKE '%'||$1||'%' OR t.name ILIKE '%'||$1||'%' OR a.email ILIKE '%'||$1||'%')
 ORDER BY t.created_at DESC,t.id DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformWorkspace{}
	for rows.Next() {
		var x PlatformWorkspace
		if err = rows.Scan(&x.ID, &x.Slug, &x.Name, &x.AdminEmail, &x.Members, &x.Sources, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) ListPlatformAuditEvents(ctx context.Context, query string, limit int) ([]PlatformAuditEvent, error) {
	query = strings.TrimSpace(query)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `SELECT e.id,e.occurred_at,e.actor_user_id,COALESCE(u.email,''),e.action,e.resource_type,e.resource_id,e.metadata
 FROM platform_audit_events e LEFT JOIN users u ON u.id=e.actor_user_id
 WHERE ($1='' OR e.action ILIKE '%'||$1||'%' OR e.resource_type ILIKE '%'||$1||'%' OR e.resource_id ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%')
 ORDER BY e.occurred_at DESC,e.id DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatformAuditEvent{}
	for rows.Next() {
		var event PlatformAuditEvent
		if err = rows.Scan(&event.ID, &event.OccurredAt, &event.ActorUserID, &event.ActorEmail, &event.Action, &event.ResourceType, &event.ResourceID, &event.Metadata); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) SetPlatformAdmin(ctx context.Context, actor, user string, enabled bool) error {
	if _, err := uuid.Parse(actor); err != nil {
		return invalid("invalid actor ID")
	}
	if _, err := uuid.Parse(user); err != nil {
		return invalid("invalid user ID")
	}
	if !enabled && actor == user {
		return ErrPlatformSelfRevoke
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901258)`); err != nil {
		return err
	}
	var userEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&userEnabled); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if enabled && !userEnabled {
		return ErrConflict
	}
	var currentlyEnabled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_admins WHERE user_id=$1 AND enabled)`, user).Scan(&currentlyEnabled); err != nil {
		return err
	}
	if currentlyEnabled == enabled {
		return tx.Commit(ctx)
	}
	if !enabled {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM platform_admins p JOIN users u ON u.id=p.user_id WHERE p.enabled AND u.enabled`).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return ErrLastPlatformAdmin
		}
		if _, err = tx.Exec(ctx, `UPDATE platform_admins SET enabled=false,updated_at=now() WHERE user_id=$1`, user); err != nil {
			return err
		}
	} else if _, err = tx.Exec(ctx, `INSERT INTO platform_admins(user_id,enabled,granted_by)
 VALUES($1,true,$2) ON CONFLICT(user_id) DO UPDATE SET enabled=true,granted_by=excluded.granted_by,updated_at=now()`, user, actor); err != nil {
		return err
	}
	action := "platform_admin.revoke"
	if enabled {
		action = "platform_admin.grant"
	}
	metadata, _ := json.Marshal(map[string]any{"enabled": enabled, "source": "web"})
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audit_events(actor_user_id,action,resource_type,resource_id,metadata) VALUES($1,$2,'user',$3,$4)`, actor, action, user, metadata); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreatePlatformWorkspace(ctx context.Context, actor, adminEmail, name, slug string) (PlatformWorkspace, error) {
	if _, err := uuid.Parse(actor); err != nil {
		return PlatformWorkspace{}, invalid("invalid actor ID")
	}
	adminEmail, err := NormalizeEmail(adminEmail)
	if err != nil {
		return PlatformWorkspace{}, err
	}
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if name == "" || len(name) > 128 || !validWorkspaceSlug(slug) {
		return PlatformWorkspace{}, invalid("invalid workspace name or slug")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlatformWorkspace{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901257)`); err != nil {
		return PlatformWorkspace{}, err
	}
	var adminID string
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 AND enabled FOR SHARE`, adminEmail).Scan(&adminID); errors.Is(err, pgx.ErrNoRows) {
		return PlatformWorkspace{}, ErrNotFound
	} else if err != nil {
		return PlatformWorkspace{}, err
	}
	workspace := PlatformWorkspace{ID: uuid.NewString(), Slug: slug, Name: name, AdminEmail: adminEmail, Members: 1}
	if err = tx.QueryRow(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3) RETURNING created_at`, workspace.ID, slug, name).Scan(&workspace.CreatedAt); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return PlatformWorkspace{}, ErrConflict
		}
		return PlatformWorkspace{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'admin')`, workspace.ID, adminID); err != nil {
		return PlatformWorkspace{}, err
	}
	metadata, _ := json.Marshal(map[string]string{"slug": slug, "name": name, "admin_email": adminEmail})
	if _, err = appendAuditTx(ctx, tx, workspace.ID, auditInput(AuditActor{Type: "user", ID: actor}, "workspace.create", "tenant", workspace.ID, metadata)); err != nil {
		return PlatformWorkspace{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audit_events(actor_user_id,action,resource_type,resource_id,metadata) VALUES($1,'workspace.create','tenant',$2,$3)`, actor, workspace.ID, metadata); err != nil {
		return PlatformWorkspace{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlatformWorkspace{}, err
	}
	return workspace, nil
}

func (s *Store) SetPlatformUserEnabled(ctx context.Context, actor, user string, enabled bool) error {
	if _, err := uuid.Parse(user); err != nil {
		return invalid("invalid user ID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901258)`); err != nil {
		return err
	}
	if actor == user && !enabled {
		return ErrPlatformSelfDisable
	}
	var admin bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_admins WHERE user_id=$1 AND enabled) FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&admin); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !enabled && admin {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM platform_admins p JOIN users u ON u.id=p.user_id WHERE p.enabled AND u.enabled`).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return ErrLastPlatformAdmin
		}
	}
	if !enabled {
		var workspaceAdmin bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id=$1 AND enabled AND role='admin')`, user).Scan(&workspaceAdmin); err != nil {
			return err
		}
		if workspaceAdmin {
			return ErrWorkspaceAdminDisable
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET enabled=$2,updated_at=now() WHERE id=$1`, user, enabled); err != nil {
		return err
	}
	if !enabled {
		if _, err = tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, user); err != nil {
			return err
		}
	}
	meta, _ := json.Marshal(map[string]any{"enabled": enabled})
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audit_events(actor_user_id,action,resource_type,resource_id,metadata) VALUES($1,'user.enabled.set','user',$2,$3)`, actor, user, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RevokePlatformUserSessions(ctx context.Context, actor, user string) error {
	if _, err := uuid.Parse(user); err != nil {
		return invalid("invalid user ID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, user)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, user).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	meta, _ := json.Marshal(map[string]any{"sessions_revoked": result.RowsAffected()})
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audit_events(actor_user_id,action,resource_type,resource_id,metadata) VALUES($1,'user.sessions.revoke','user',$2,$3)`, actor, user, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
