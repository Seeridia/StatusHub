package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type TeamCipher interface {
	Encode(json.RawMessage) (string, error)
	Decode(string) (json.RawMessage, error)
}
type TeamCommand struct {
	Action    string    `json:"action"`
	PublicURL string    `json:"-"`
	ID        string    `json:"id,omitempty"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Role      auth.Role `json:"role,omitempty"`
	Enabled   bool      `json:"enabled"`
}
type Member struct {
	ID      string    `json:"id"`
	Email   string    `json:"email"`
	Name    string    `json:"name"`
	Role    auth.Role `json:"role"`
	Enabled bool      `json:"enabled"`
	Method  string    `json:"method"`
}
type Invitation struct {
	ID         string     `json:"id"`
	MailStatus string     `json:"mail_status"`
	Email      string     `json:"email"`
	Role       auth.Role  `json:"role"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func NormalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, e := mail.ParseAddress(email)
	if e != nil || a.Address != email || len(email) > 254 {
		return "", invalid("invalid email")
	}
	return email, nil
}
func identityToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenDigest(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func lockTeam(ctx context.Context, tx pgx.Tx, tenant string) error {
	var id string
	return tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&id)
}
func currentActor(ctx context.Context, tx pgx.Tx, actor auth.Identity) (auth.Identity, error) {
	var e error
	if actor.ActorType == "service_account" {
		e = tx.QueryRow(ctx, `SELECT role FROM service_accounts WHERE id=$1 AND tenant_id=$2 AND enabled`, actor.ActorID, actor.TenantID).Scan(&actor.Role)
	} else {
		e = tx.QueryRow(ctx, `SELECT m.role,p.email FROM memberships m JOIN users p ON p.id=m.user_id WHERE m.user_id=$1 AND m.tenant_id=$2 AND m.enabled`, actor.ActorID, actor.TenantID).Scan(&actor.Role, &actor.Email)
	}
	if e != nil {
		return actor, auth.ErrUnauthenticated
	}
	return actor, nil
}
func (s *Store) TeamList(ctx context.Context, actor auth.Identity, kind string) (any, error) {
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	actor, e = currentActor(ctx, tx, actor)
	if e != nil {
		return nil, e
	}
	if actor.Role != auth.RoleAdmin && actor.Role != auth.RoleOwner {
		return nil, auth.ErrForbidden
	}
	switch kind {
	case "members":
		rows, e := tx.Query(ctx, `SELECT p.id,COALESCE(p.email,''),COALESCE(p.display_name,''),m.role,m.enabled,'password' FROM memberships m JOIN users p ON p.id=m.user_id WHERE m.tenant_id=$1 ORDER BY p.id LIMIT 1000`, actor.TenantID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []Member{}
		for rows.Next() {
			var x Member
			if e = rows.Scan(&x.ID, &x.Email, &x.Name, &x.Role, &x.Enabled, &x.Method); e != nil {
				return nil, e
			}
			out = append(out, x)
		}
		return out, rows.Err()
	case "invitations":
		rows, e := tx.Query(ctx, `SELECT i.id,i.email,i.role,i.expires_at,i.accepted_at,i.revoked_at,
 CASE WHEN i.accepted_at IS NOT NULL THEN 'accepted' WHEN i.revoked_at IS NOT NULL THEN 'revoked' WHEN i.expires_at<=now() THEN 'expired'
 WHEN j.completed_at IS NOT NULL THEN 'submitted' WHEN j.payload IS NULL OR j.attempts>=6 THEN 'failed' ELSE 'pending' END
 FROM team_invitations i LEFT JOIN LATERAL (SELECT * FROM identity_mail_jobs WHERE invitation_id=i.id ORDER BY created_at DESC LIMIT 1) j ON true WHERE i.tenant_id=$1 ORDER BY i.created_at DESC LIMIT 1000`, actor.TenantID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []Invitation{}
		for rows.Next() {
			var x Invitation
			if e = rows.Scan(&x.ID, &x.Email, &x.Role, &x.ExpiresAt, &x.AcceptedAt, &x.RevokedAt, &x.MailStatus); e != nil {
				return nil, e
			}
			out = append(out, x)
		}
		return out, rows.Err()
	case "service-accounts":
		rows, e := tx.Query(ctx, `SELECT id,tenant_id,name,role,enabled,last_used_at FROM service_accounts WHERE tenant_id=$1 ORDER BY name LIMIT 1000`, actor.TenantID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []ServiceAccount{}
		for rows.Next() {
			var x ServiceAccount
			if e = rows.Scan(&x.ID, &x.TenantID, &x.Name, &x.Role, &x.Enabled, &x.LastUsedAt); e != nil {
				return nil, e
			}
			out = append(out, x)
		}
		return out, rows.Err()
	}
	return nil, invalid("unknown identity resource")
}
func (s *Store) TeamMutate(ctx context.Context, actor auth.Identity, key string, c TeamCommand, cipher TeamCipher) (json.RawMessage, error) {
	if c.ID != "" {
		if _, err := uuid.Parse(c.ID); err != nil {
			return nil, invalid("invalid identity resource ID")
		}
	}
	if len(key) < 1 || len(key) > 128 {
		return nil, invalid("Idempotency-Key is required")
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = lockTeam(ctx, tx, actor.TenantID); e != nil {
		return nil, e
	}
	actor, e = currentActor(ctx, tx, actor)
	if e != nil {
		return nil, e
	}
	if actor.Role != auth.RoleAdmin && actor.Role != auth.RoleOwner {
		return nil, auth.ErrForbidden
	}
	b, _ := json.Marshal(struct {
		Actor   string
		Type    string
		Role    auth.Role
		Command TeamCommand
	}{actor.ActorID, actor.ActorType, actor.Role, c})
	hash := tokenDigest(string(b))
	var oldHash []byte
	var result json.RawMessage
	var sealed *string
	var until *time.Time
	e = tx.QueryRow(ctx, `SELECT request_hash,result,secret_result,secret_until FROM identity_mutations WHERE tenant_id=$1 AND key=$2`, actor.TenantID, key).Scan(&oldHash, &result, &sealed, &until)
	if e == nil {
		if string(oldHash) != string(hash) {
			return nil, ErrConflict
		}
		// Replayed secrets remain subject to the target's current role.
		if strings.HasPrefix(c.Action, "service.") {
			id := c.ID
			if id == "" {
				var saved struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(result, &saved); err != nil {
					return nil, err
				}
				id = saved.ID
			}
			var role auth.Role
			if err := tx.QueryRow(ctx, `SELECT role FROM service_accounts WHERE id=$1 AND tenant_id=$2`, id, actor.TenantID).Scan(&role); err != nil {
				return nil, ErrNotFound
			}
			if !auth.CanManage(actor.Role, role, role) {
				return nil, auth.ErrForbidden
			}
		}
		if until != nil {
			if sealed == nil || !time.Now().Before(*until) {
				return json.RawMessage(`{"completed":true,"secret_unavailable":true}`), nil
			}
			return cipher.Decode(*sealed)
		}
		return result, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	secret := ""
	out := map[string]any{"completed": true}
	oldRole := auth.RoleViewer
	switch c.Action {
	case "member.update":
		var enabled bool
		if e = tx.QueryRow(ctx, `SELECT role,enabled FROM memberships WHERE tenant_id=$1 AND user_id=$2`, actor.TenantID, c.ID).Scan(&oldRole, &enabled); e != nil {
			return nil, ErrNotFound
		}
		if actor.ActorType == "user" && actor.ActorID == c.ID {
			return nil, auth.ErrForbidden
		}
		if !auth.CanManage(actor.Role, oldRole, c.Role) {
			return nil, auth.ErrForbidden
		}
		if oldRole == auth.RoleOwner && enabled && (c.Role != auth.RoleOwner || !c.Enabled) {
			var count int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE tenant_id=$1 AND role='owner' AND enabled`, actor.TenantID).Scan(&count); e != nil {
				return nil, e
			}
			if count <= 1 {
				return nil, ErrConflict
			}
		}
		_, e = tx.Exec(ctx, `UPDATE memberships SET role=$3,enabled=$4,updated_at=now() WHERE tenant_id=$1 AND user_id=$2`, actor.TenantID, c.ID, c.Role, c.Enabled)
	case "invitation.create":
		if !auth.CanManage(actor.Role, auth.RoleViewer, c.Role) {
			return nil, auth.ErrForbidden
		}
		c.Email, e = NormalizeEmail(c.Email)
		if e != nil {
			return nil, e
		}
		if actor.Role != auth.RoleOwner {
			var elevated bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_invitations WHERE tenant_id=$1 AND email=$2 AND role IN ('admin','owner') AND revoked_at IS NULL AND accepted_at IS NULL AND expires_at>now())`, actor.TenantID, c.Email).Scan(&elevated); e != nil {
				return nil, e
			}
			if elevated {
				return nil, auth.ErrForbidden
			}
		}
		_, e = tx.Exec(ctx, `UPDATE team_invitations SET revoked_at=now() WHERE tenant_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, actor.TenantID, c.Email)
		if e != nil {
			return nil, e
		}
		c.ID = uuid.NewString()
		inviteToken := identityToken()
		_, e = tx.Exec(ctx, `INSERT INTO team_invitations(id,tenant_id,email,role,token_hash,actor_type,actor_id,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '7 days')`, c.ID, actor.TenantID, c.Email, c.Role, tokenDigest(inviteToken), actor.ActorType, actor.ActorID)
		if e == nil {
			e = queueMail(ctx, tx, c.Email, "invite", c.PublicURL+"/ui/#/invitation?token="+inviteToken, &c.ID, time.Now().Add(7*24*time.Hour), cipher)
		}
		out["mail_status"] = "pending"
	case "invitation.revoke":
		if e = tx.QueryRow(ctx, `SELECT role FROM team_invitations WHERE tenant_id=$1 AND id=$2`, actor.TenantID, c.ID).Scan(&oldRole); e != nil {
			return nil, ErrNotFound
		}
		if !auth.CanManage(actor.Role, oldRole, oldRole) {
			return nil, auth.ErrForbidden
		}
		_, e = tx.Exec(ctx, `UPDATE team_invitations SET revoked_at=now() WHERE tenant_id=$1 AND id=$2 AND accepted_at IS NULL`, actor.TenantID, c.ID)
	case "service.create", "service.update", "service.rotate":
		if c.Action != "service.create" {
			if e = tx.QueryRow(ctx, `SELECT role FROM service_accounts WHERE tenant_id=$1 AND id=$2`, actor.TenantID, c.ID).Scan(&oldRole); e != nil {
				return nil, ErrNotFound
			}
			if c.Action == "service.rotate" {
				c.Role = oldRole
			}
			if actor.ActorType == "service_account" && actor.ActorID == c.ID {
				return nil, auth.ErrForbidden
			}
		}
		if !auth.CanManage(actor.Role, oldRole, c.Role) {
			return nil, auth.ErrForbidden
		}
		if c.Action == "service.create" {
			c.Name = strings.TrimSpace(c.Name)
			if c.Name == "" || len(c.Name) > 128 {
				return nil, invalid("name must contain 1–128 bytes")
			}
			c.ID = uuid.NewString()
		}
		if c.Action != "service.update" {
			secret = "sa." + c.ID + "." + identityToken()
			out["token"] = secret
		}
		switch c.Action {
		case "service.create":
			_, e = tx.Exec(ctx, `INSERT INTO service_accounts(id,tenant_id,name,role,token_hash) VALUES($1,$2,$3,$4,$5)`, c.ID, actor.TenantID, c.Name, c.Role, tokenDigest(secret))
		case "service.rotate":
			_, e = tx.Exec(ctx, `UPDATE service_accounts SET token_hash=$3,updated_at=now() WHERE tenant_id=$1 AND id=$2`, actor.TenantID, c.ID, tokenDigest(secret))
		case "service.update":
			_, e = tx.Exec(ctx, `UPDATE service_accounts SET role=$3,enabled=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2`, actor.TenantID, c.ID, c.Role, c.Enabled)
		}
	default:
		return nil, invalid("unknown identity action")
	}
	if e != nil {
		var pgerr *pgconn.PgError
		if errors.As(e, &pgerr) && pgerr.Code == "23505" {
			return nil, ErrConflict
		}
		return nil, e
	}
	out["id"] = c.ID
	metadata, _ := json.Marshal(map[string]any{"role": c.Role, "previous_role": oldRole, "enabled": c.Enabled})
	if _, e = appendAuditTx(ctx, tx, actor.TenantID, auditInput(AuditActor{Type: actor.ActorType, ID: actor.ActorID}, "identity."+c.Action, "identity", c.ID, metadata)); e != nil {
		return nil, e
	}
	result, _ = json.Marshal(out)
	var encrypted *string
	var expiry *time.Time
	stored := result
	if secret != "" {
		v, err := cipher.Encode(result)
		if err != nil {
			return nil, err
		}
		encrypted = &v
		t := time.Now().Add(10 * time.Minute)
		expiry = &t
		stored, _ = json.Marshal(map[string]any{"completed": true, "id": c.ID})
	}
	_, e = tx.Exec(ctx, `INSERT INTO identity_mutations(tenant_id,key,request_hash,result,secret_result,secret_until) VALUES($1,$2,$3,$4,$5,$6)`, actor.TenantID, key, hash, stored, encrypted, expiry)
	if e != nil {
		return nil, e
	}
	return result, tx.Commit(ctx)
}
