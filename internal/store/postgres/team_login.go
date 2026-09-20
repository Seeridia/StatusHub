package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

var (
	ErrRateLimited       = errors.New("identity rate limit exceeded")
	ErrInvitationInvalid = errors.New("invitation unavailable")
	ErrSetupUnavailable  = errors.New("setup unavailable")
	ErrPasswordInvalid   = errors.New("invalid password")
	ErrEmailMismatch     = errors.New("invitation email mismatch")
	ErrLoginRequired     = errors.New("existing account must sign in")
)

type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	VerifiedAt      *time.Time `json:"email_verified_at"`
	PasswordVersion string     `json:"-"`
}
type Workspace struct {
	ID   string    `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
	Role auth.Role `json:"role"`
}
type BrowserSession struct {
	User      User      `json:"user"`
	CSRF      string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Store) IdentityRateLimit(ctx context.Context, key string, limit int, window time.Duration) error {
	var n int
	e := s.db.QueryRow(ctx, `INSERT INTO identity_rate_limits(key,window_start,attempts) VALUES($1,now(),1)
 ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN identity_rate_limits.window_start<now()-make_interval(secs=>$2) THEN 1 ELSE identity_rate_limits.attempts+1 END,
 window_start=CASE WHEN identity_rate_limits.window_start<now()-make_interval(secs=>$2) THEN now() ELSE identity_rate_limits.window_start END RETURNING attempts`, key, window.Seconds()).Scan(&n)
	if e != nil {
		return e
	}
	if n > limit {
		return ErrRateLimited
	}
	return nil
}
func scanUser(row pgx.Row) (User, string, error) {
	var u User
	var hash string
	e := row.Scan(&u.ID, &u.Email, &u.Name, &u.VerifiedAt, &u.PasswordVersion, &hash)
	return u, hash, e
}

const userColumns = "id,email,display_name,email_verified_at,password_version::text,password_hash"

func (s *Store) PasswordUser(ctx context.Context, email, password string) (User, error) {
	email, e := NormalizeEmail(email)
	if e != nil {
		return User{}, auth.ErrUnauthenticated
	}
	u, h, e := scanUser(s.db.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE email=$1 AND enabled", email))
	if e != nil {
		auth.VerifyPassword(dummyPasswordHash, password)
		return User{}, auth.ErrUnauthenticated
	}
	if !auth.VerifyPassword(h, password) {
		return User{}, auth.ErrUnauthenticated
	}
	return u, nil
}

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func (s *Store) UserWorkspaces(ctx context.Context, user string) ([]Workspace, error) {
	rows, e := s.db.Query(ctx, `SELECT t.id,t.slug,t.name,m.role FROM memberships m JOIN tenants t ON t.id=m.tenant_id JOIN users u ON u.id=m.user_id WHERE m.user_id=$1 AND m.enabled AND u.enabled ORDER BY t.name,t.id`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		if e = rows.Scan(&w.ID, &w.Slug, &w.Name, &w.Role); e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Store) MemberIdentity(ctx context.Context, user, tenant string) (auth.Identity, error) {
	a := auth.Identity{ActorType: "user", ActorID: user, TenantID: tenant}
	e := s.db.QueryRow(ctx, `SELECT m.role,u.email FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.user_id=$1 AND m.tenant_id=$2 AND m.enabled AND u.enabled`, user, tenant).Scan(&a.Role, &a.Email)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, auth.ErrForbidden
	}
	return a, e
}
func (s *Store) CreateUserSession(ctx context.Context, token, csrf string, u User, expires time.Time) error {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var version string
	if e = tx.QueryRow(ctx, `SELECT password_version::text FROM users WHERE id=$1 FOR SHARE`, u.ID).Scan(&version); e != nil {
		return e
	}
	if version != u.PasswordVersion {
		return auth.ErrUnauthenticated
	}
	_, e = tx.Exec(ctx, `INSERT INTO browser_sessions(token_hash,user_id,csrf,expires_at) VALUES($1,$2,$3,$4)`, tokenDigest(token), u.ID, csrf, expires)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) UserSession(ctx context.Context, token string) (BrowserSession, error) {
	var b BrowserSession
	e := s.db.QueryRow(ctx, `SELECT u.id,u.email,u.display_name,u.email_verified_at,u.password_version::text,s.csrf,s.expires_at
 FROM browser_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND u.enabled AND s.revoked_at IS NULL AND s.expires_at>now() AND s.last_active_at>now()-interval '24 hours'`, tokenDigest(token)).Scan(
		&b.User.ID, &b.User.Email, &b.User.Name, &b.User.VerifiedAt, &b.User.PasswordVersion, &b.CSRF, &b.ExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return b, auth.ErrUnauthenticated
	}
	if e != nil {
		return b, e
	}
	_, e = s.db.Exec(ctx, `UPDATE browser_sessions SET last_active_at=now() WHERE token_hash=$1 AND last_active_at<now()-interval '5 minutes' AND revoked_at IS NULL AND expires_at>now() AND last_active_at>now()-interval '24 hours'`, tokenDigest(token))
	return b, e
}
func auditUser(ctx context.Context, tx pgx.Tx, user, action string) error {
	rows, e := tx.Query(ctx, `SELECT tenant_id FROM memberships WHERE user_id=$1 ORDER BY tenant_id`, user)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = appendAuditTx(ctx, tx, id, auditInput(AuditActor{Type: "user", ID: user}, action, "user", user, json.RawMessage("{}"))); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) RevokeUserSessions(ctx context.Context, user, token string) error {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE user_id=$1 AND ($2='' OR token_hash=$3)`, user, token, tokenDigest(token))
	if e != nil {
		return e
	}
	if e = auditUser(ctx, tx, user, "identity.session.logout"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) SetupLink(ctx context.Context) (string, error) {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901257)`); e != nil {
		return "", e
	}
	var occupied bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants) OR EXISTS(SELECT 1 FROM users)`).Scan(&occupied); e != nil {
		return "", e
	}
	if occupied {
		return "", ErrSetupUnavailable
	}
	token := identityToken()
	_, e = tx.Exec(ctx, `INSERT INTO setup_tokens(singleton,token_hash,expires_at) VALUES(true,$1,now()+interval '30 minutes') ON CONFLICT(singleton) DO UPDATE SET token_hash=excluded.token_hash,expires_at=excluded.expires_at`, tokenDigest(token))
	if e != nil {
		return "", e
	}
	return token, tx.Commit(ctx)
}
func (s *Store) CompleteSetup(ctx context.Context, token, email, password, name, slug string) (User, error) {
	email, e := NormalizeEmail(email)
	if e != nil {
		return User{}, e
	}
	name = strings.TrimSpace(name)
	slug = strings.TrimSpace(slug)
	if name == "" || len(name) > 128 || !validWorkspaceSlug(slug) {
		return User{}, invalid("invalid workspace")
	}
	h, e := auth.HashPassword(password)
	if e != nil {
		return User{}, ErrPasswordInvalid
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return User{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901257)`); e != nil {
		return User{}, e
	}
	var available bool
	e = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM tenants) AND NOT EXISTS(SELECT 1 FROM users) AND EXISTS(SELECT 1 FROM setup_tokens WHERE token_hash=$1 AND expires_at>now())`, tokenDigest(token)).Scan(&available)
	if e != nil {
		return User{}, e
	}
	if !available {
		return User{}, ErrSetupUnavailable
	}
	u := User{ID: uuid.NewString(), Email: email, PasswordVersion: uuid.NewString()}
	tenant := uuid.NewString()
	if _, e = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,password_version) VALUES($1,$2,$3,$4)`, u.ID, email, h, u.PasswordVersion); e != nil {
		return User{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3)`, tenant, slug, name); e != nil {
		return User{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'admin')`, tenant, u.ID); e != nil {
		return User{}, e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM setup_tokens`); e != nil {
		return User{}, e
	}
	if e = auditUser(ctx, tx, u.ID, "identity.setup"); e != nil {
		return User{}, e
	}
	return u, tx.Commit(ctx)
}
func validWorkspaceSlug(v string) bool {
	if len(v) < 1 || len(v) > 63 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return v[0] != '-' && v[len(v)-1] != '-'
}
func (s *Store) CreateAdminWorkspace(ctx context.Context, email, name, slug string) (Workspace, error) {
	email, e := NormalizeEmail(email)
	if e != nil {
		return Workspace{}, e
	}
	if !validWorkspaceSlug(slug) || strings.TrimSpace(name) == "" || len(name) > 128 {
		return Workspace{}, invalid("invalid workspace")
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return Workspace{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(734901257)`); e != nil {
		return Workspace{}, e
	}
	var user string
	if e = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&user); e != nil {
		return Workspace{}, ErrNotFound
	}
	w := Workspace{ID: uuid.NewString(), Slug: slug, Name: name, Role: auth.RoleAdmin}
	if _, e = tx.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3)`, w.ID, slug, name); e != nil {
		return w, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'admin')`, w.ID, user); e != nil {
		return w, e
	}
	if _, e = appendAuditTx(ctx, tx, w.ID, auditInput(AuditActor{Type: "system", ID: "statushub-admin"}, "workspace.create", "tenant", w.ID, json.RawMessage("{}"))); e != nil {
		return w, e
	}
	return w, tx.Commit(ctx)
}

type InvitationPreview struct {
	Email        string    `json:"email"`
	Workspace    Workspace `json:"workspace"`
	ExistingUser bool      `json:"existing_user"`
}

func validInvitation(ctx context.Context, tx pgx.Tx, token string) (Invitation, string, error) {
	var i Invitation
	var tenant, actorType, actorID string
	e := tx.QueryRow(ctx, `SELECT id,tenant_id,email,role,expires_at,actor_type,actor_id FROM team_invitations WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, tokenDigest(token)).Scan(&i.ID, &tenant, &i.Email, &i.Role, &i.ExpiresAt, &actorType, &actorID)
	if e != nil {
		return i, tenant, ErrInvitationInvalid
	}
	a, e := currentActor(ctx, tx, auth.Identity{TenantID: tenant, ActorID: actorID, ActorType: actorType})
	if e != nil || !auth.CanManage(a.Role, auth.RoleViewer, i.Role) {
		return i, tenant, ErrInvitationInvalid
	}
	return i, tenant, nil
}
func (s *Store) PreviewInvitation(ctx context.Context, token string) (InvitationPreview, error) {
	var p InvitationPreview
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return p, e
	}
	defer tx.Rollback(ctx)
	i, t, e := validInvitation(ctx, tx, token)
	if e != nil {
		return p, e
	}
	p.Email = i.Email
	p.Workspace.Role = i.Role
	e = tx.QueryRow(ctx, `SELECT id,slug,name FROM tenants WHERE id=$1`, t).Scan(&p.Workspace.ID, &p.Workspace.Slug, &p.Workspace.Name)
	if e != nil {
		return p, e
	}
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, i.Email).Scan(&p.ExistingUser)
	return p, e
}
func (s *Store) AcceptInvitation(ctx context.Context, token, password, sessionUser, sessionToken string) (User, error) {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return User{}, e
	}
	defer tx.Rollback(ctx)
	var tenant string
	if e = tx.QueryRow(ctx, `SELECT tenant_id FROM team_invitations WHERE token_hash=$1`, tokenDigest(token)).Scan(&tenant); e != nil {
		return User{}, ErrInvitationInvalid
	}
	if e = lockTeam(ctx, tx, tenant); e != nil {
		return User{}, e
	}
	i, _, e := validInvitation(ctx, tx, token)
	if e != nil {
		return User{}, e
	}
	// Serialize registration for the same email across invitations in different workspaces.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,42))`, i.Email); e != nil {
		return User{}, e
	}
	u, _, e := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE email=$1 FOR UPDATE", i.Email))
	if errors.Is(e, pgx.ErrNoRows) {
		if sessionUser != "" {
			return User{}, ErrEmailMismatch
		}
		h, err := auth.HashPassword(password)
		if err != nil {
			return User{}, ErrPasswordInvalid
		}
		u = User{ID: uuid.NewString(), Email: i.Email, PasswordVersion: uuid.NewString()}
		_, e = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,password_version,email_verified_at) VALUES($1,$2,$3,$4,now())`, u.ID, u.Email, h, u.PasswordVersion)
	} else if e == nil {
		if sessionUser == "" {
			return User{}, ErrLoginRequired
		}
		if sessionUser != u.ID {
			return User{}, ErrEmailMismatch
		}
		// Password reset locks this user too; do not mint a session from a revoked invitation acceptance request.
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sessions WHERE token_hash=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now() AND last_active_at>now()-interval '24 hours')`, tokenDigest(sessionToken), u.ID).Scan(&active); err != nil {
			return User{}, err
		}
		if !active {
			return User{}, auth.ErrUnauthenticated
		}
	}
	if e != nil {
		return User{}, e
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE tenant_id=$1 AND user_id=$2)`, tenant, u.ID).Scan(&exists); e != nil {
		return User{}, e
	}
	if exists {
		return User{}, ErrConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,$3)`, tenant, u.ID, i.Role); e != nil {
		return User{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()) WHERE id=$1`, u.ID); e != nil {
		return User{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE team_invitations SET accepted_at=now() WHERE id=$1`, i.ID); e != nil {
		return User{}, e
	}
	if _, e = appendAuditTx(ctx, tx, tenant, auditInput(AuditActor{Type: "user", ID: u.ID}, "identity.invitation.accept", "user", u.ID, json.RawMessage("{}"))); e != nil {
		return User{}, e
	}
	return u, tx.Commit(ctx)
}
func queueMail(ctx context.Context, tx pgx.Tx, email, purpose, url string, invitation *string, expires time.Time, cipher TeamCipher) error {
	p, _ := json.Marshal(map[string]string{"to": email, "purpose": purpose, "url": url})
	sealed, e := cipher.Encode(p)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO identity_mail_jobs(id,invitation_id,payload,expires_at) VALUES($1,$2,$3,$4)`, uuid.NewString(), invitation, sealed, expires)
	return e
}
func (s *Store) RequestUserMail(ctx context.Context, purpose, email, publicURL string, cipher TeamCipher) error {
	email, e := NormalizeEmail(email)
	if e != nil {
		return nil
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 FOR UPDATE`, email).Scan(&id); errors.Is(e, pgx.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if purpose != "reset" && purpose != "verify-email" {
		return invalid("invalid mail purpose")
	}
	token := identityToken()
	expires := time.Now().Add(30 * time.Minute)
	if _, e = tx.Exec(ctx, `UPDATE identity_tokens SET consumed_at=now() WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL`, id, purpose); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO identity_tokens(token_hash,purpose,email,user_id,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenDigest(token), purpose, email, id, expires); e != nil {
		return e
	}
	path := "/ui/#/reset-password?token="
	if purpose == "verify-email" {
		path = "/ui/#/verify-email?token="
	}
	if e = queueMail(ctx, tx, email, purpose, publicURL+path+token, nil, expires, cipher); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) CompleteUserToken(ctx context.Context, token, purpose, password string) error {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, `SELECT user_id FROM identity_tokens WHERE token_hash=$1`, tokenDigest(token)).Scan(&id); e != nil {
		return auth.ErrUnauthenticated
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id); e != nil {
		return e
	}
	var valid bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_tokens WHERE token_hash=$1 AND purpose=$2 AND consumed_at IS NULL AND expires_at>now())`, tokenDigest(token), purpose).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return auth.ErrUnauthenticated
	}
	if purpose == "reset" {
		h, err := auth.HashPassword(password)
		if err != nil {
			return ErrPasswordInvalid
		}
		if _, e = tx.Exec(ctx, `UPDATE users SET password_hash=$2,password_version=$3,updated_at=now() WHERE id=$1`, id, h, uuid.NewString()); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE user_id=$1`, id); e != nil {
			return e
		}
	} else if purpose != "verify-email" {
		return invalid("invalid token purpose")
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()) WHERE id=$1`, id); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_tokens SET consumed_at=now() WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL`, id, purpose); e != nil {
		return e
	}
	if e = auditUser(ctx, tx, id, "identity."+purpose); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) ChangeUserPassword(ctx context.Context, user, old, password string) error {
	h, e := auth.HashPassword(password)
	if e != nil {
		return ErrPasswordInvalid
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var expected string
	if e = tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 FOR UPDATE`, user).Scan(&expected); e != nil {
		return e
	}
	if !auth.VerifyPassword(expected, old) {
		return auth.ErrUnauthenticated
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET password_hash=$2,password_version=$3,updated_at=now() WHERE id=$1`, user, h, uuid.NewString()); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE user_id=$1`, user); e != nil {
		return e
	}
	if e = auditUser(ctx, tx, user, "identity.password.change"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
