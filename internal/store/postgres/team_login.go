package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Invite tokens authorize a specific membership, never ownership of an email.
func validInvitation(ctx context.Context, tx pgx.Tx, token string) (Invitation, string, error) {
	var i Invitation
	var tenant, actorType, actorID string
	e := tx.QueryRow(ctx, `SELECT id,tenant_id,email,role,expires_at,actor_type,actor_id FROM team_invitations WHERE token_hash=$1 AND revoked_at IS NULL AND accepted_at IS NULL AND expires_at>now() FOR UPDATE`, tokenDigest(token)).Scan(&i.ID, &tenant, &i.Email, &i.Role, &i.ExpiresAt, &actorType, &actorID)
	if e != nil {
		return i, tenant, auth.ErrUnauthenticated
	}
	if actorType == "bootstrap" {
		var n int
		e = tx.QueryRow(ctx, `SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND role='owner' AND enabled`, tenant).Scan(&n)
		if e != nil || n != 0 {
			return i, tenant, auth.ErrForbidden
		}
	} else {
		actor, e := currentActor(ctx, tx, auth.Identity{TenantID: tenant, ActorType: actorType, ActorID: actorID})
		if e != nil || !auth.CanManage(actor.Role, auth.RoleViewer, i.Role) {
			return i, tenant, auth.ErrForbidden
		}
	}
	return i, tenant, nil
}
func bindInvitation(ctx context.Context, tx pgx.Tx, i Invitation, tenant, principal string) error {
	var exists bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_memberships WHERE tenant_id=$1 AND principal_id=$2)`, tenant, principal).Scan(&exists); e != nil {
		return e
	}
	// Invitations never reactivate or change an existing membership.
	if exists {
		return ErrConflict
	}
	if _, e := tx.Exec(ctx, `INSERT INTO tenant_memberships(tenant_id,principal_id,role) VALUES($1,$2,$3)`, tenant, principal, i.Role); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `UPDATE team_invitations SET accepted_at=now() WHERE id=$1`, i.ID); e != nil {
		return e
	}
	_, e := appendAuditTx(ctx, tx, tenant, auditInput(AuditActor{Type: "user", ID: principal}, "identity.invitation.accept", "principal", principal, json.RawMessage(`{}`)))
	return e
}
func (s *Store) BootstrapInvitation(ctx context.Context, tenant, email string) (string, error) {
	email, e := NormalizeEmail(email)
	if e != nil {
		return "", e
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if e = lockTeam(ctx, tx, tenant); e != nil {
		return "", e
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND role='owner' AND enabled`, tenant).Scan(&n); e != nil {
		return "", e
	}
	if n != 0 {
		return "", ErrConflict
	}
	if _, e = tx.Exec(ctx, `UPDATE team_invitations SET revoked_at=now() WHERE tenant_id=$1 AND actor_type='bootstrap' AND accepted_at IS NULL`, tenant); e != nil {
		return "", e
	}
	token := identityToken()
	id := uuid.NewString()
	_, e = tx.Exec(ctx, `INSERT INTO team_invitations(id,tenant_id,email,role,token_hash,actor_type,actor_id,expires_at) VALUES($1,$2,$3,'owner',$4,'bootstrap','statushub-admin',now()+interval '7 days')`, id, tenant, email, tokenDigest(token))
	if e != nil {
		return "", e
	}
	if _, e = appendAuditTx(ctx, tx, tenant, auditInput(AuditActor{Type: "system", ID: "statushub-admin"}, "identity.owner.bootstrap", "invitation", id, json.RawMessage(`{}`))); e != nil {
		return "", e
	}
	return token, tx.Commit(ctx)
}
func (s *Store) IdentityRateLimit(ctx context.Context, key string, limit int, window time.Duration) error {
	// Database time and an atomic UPSERT make limits shared across API instances.
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var n int
	e = tx.QueryRow(ctx, `INSERT INTO identity_rate_limits(key,window_start,attempts) VALUES($1,now(),1) ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN identity_rate_limits.window_start<now()-make_interval(secs=>$2) THEN 1 ELSE identity_rate_limits.attempts+1 END,window_start=CASE WHEN identity_rate_limits.window_start<now()-make_interval(secs=>$2) THEN now() ELSE identity_rate_limits.window_start END RETURNING attempts`, key, window.Seconds()).Scan(&n)
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if n > limit {
		return ErrRateLimited
	}
	return nil
}

var ErrRateLimited = errors.New("identity rate limit exceeded")

func (s *Store) RequestIdentityMail(ctx context.Context, purpose, email, invite, publicURL string, cipher TeamCipher) error {
	email, e := NormalizeEmail(email)
	if e != nil {
		return nil
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var invitationID *string
	if purpose == "verify" {
		// Lock tenant before invitation to match administrative mutation lock order.
		var tenant string
		if e = tx.QueryRow(ctx, `SELECT tenant_id FROM team_invitations WHERE token_hash=$1`, tokenDigest(invite)).Scan(&tenant); e != nil {
			return auth.ErrUnauthenticated
		}
		if e = lockTeam(ctx, tx, tenant); e != nil {
			return e
		}
		i, _, err := validInvitation(ctx, tx, invite)
		if err != nil {
			return err
		}
		if i.Email != email {
			return auth.ErrUnauthenticated
		}
		invitationID = &i.ID
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM local_credentials WHERE email=$1)`, email).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return nil
		}
	} else if purpose == "reset" {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM local_credentials WHERE email=$1)`, email).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return nil
		}
	} else {
		return invalid("invalid email purpose")
	}
	token := identityToken()
	duration := 24 * time.Hour
	if purpose == "reset" {
		duration = 30 * time.Minute
	}
	expires := time.Now().Add(duration)
	_, e = tx.Exec(ctx, `INSERT INTO identity_tokens(token_hash,purpose,email,invitation_id,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenDigest(token), purpose, email, invitationID, expires)
	if e != nil {
		return e
	}
	payload, _ := json.Marshal(map[string]string{"to": email, "purpose": purpose, "url": strings.TrimRight(publicURL, "/") + "/ui/#/account-flow?mode=" + purpose + "&token=" + token})
	encrypted, e := cipher.Encode(payload)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO identity_mail_jobs(id,payload,expires_at) VALUES($1,$2,$3)`, uuid.NewString(), encrypted, expires)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) CompleteIdentityToken(ctx context.Context, token, purpose, password string) (string, error) {
	hash, e := auth.HashPassword(password)
	if e != nil {
		return "", e
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var email string
	var invitationID *string
	// Tenant first for verification, token rows thereafter.
	var tenant string
	if purpose == "verify" {
		e = tx.QueryRow(ctx, `SELECT i.tenant_id FROM identity_tokens t JOIN team_invitations i ON i.id=t.invitation_id WHERE t.token_hash=$1`, tokenDigest(token)).Scan(&tenant)
		if e != nil {
			return "", auth.ErrUnauthenticated
		}
		if e = lockTeam(ctx, tx, tenant); e != nil {
			return "", e
		}
	}
	e = tx.QueryRow(ctx, `SELECT email,invitation_id FROM identity_tokens WHERE token_hash=$1 AND purpose=$2 AND consumed_at IS NULL AND expires_at>now() FOR UPDATE`, tokenDigest(token), purpose).Scan(&email, &invitationID)
	if e != nil {
		return "", auth.ErrUnauthenticated
	}
	var principal string
	if purpose == "verify" && invitationID != nil {
		var i Invitation
		var actorType, actorID string
		e = tx.QueryRow(ctx, `SELECT id,email,role,expires_at,actor_type,actor_id FROM team_invitations WHERE id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, *invitationID).Scan(&i.ID, &i.Email, &i.Role, &i.ExpiresAt, &actorType, &actorID)
		if e != nil || i.Email != email {
			return "", auth.ErrUnauthenticated
		}
		if actorType == "bootstrap" {
			var n int
			e = tx.QueryRow(ctx, `SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND role='owner' AND enabled`, tenant).Scan(&n)
			if e != nil || n != 0 {
				return "", auth.ErrForbidden
			}
		} else {
			actor, e := currentActor(ctx, tx, auth.Identity{TenantID: tenant, ActorType: actorType, ActorID: actorID})
			if e != nil || !auth.CanManage(actor.Role, auth.RoleViewer, i.Role) {
				return "", auth.ErrForbidden
			}
		}
		principal = uuid.NewString()
		_, e = tx.Exec(ctx, `INSERT INTO principals(id,issuer,subject,email) VALUES($1,'local',$1::uuid::text,$2)`, principal, email)
		if e != nil {
			return "", e
		}
		_, e = tx.Exec(ctx, `INSERT INTO local_credentials(principal_id,email,password_hash) VALUES($1,$2,$3)`, principal, email, hash)
		if e != nil {
			return "", ErrConflict
		}
		if e = bindInvitation(ctx, tx, i, tenant, principal); e != nil {
			return "", e
		}
	} else if purpose == "reset" {
		e = tx.QueryRow(ctx, `SELECT principal_id FROM local_credentials WHERE email=$1 FOR UPDATE`, email).Scan(&principal)
		if e != nil {
			return "", auth.ErrUnauthenticated
		}
		_, e = tx.Exec(ctx, `UPDATE local_credentials SET password_hash=$2,updated_at=now() WHERE principal_id=$1`, principal, hash)
		if e != nil {
			return "", e
		}
		if _, e = tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE principal_id=$1`, principal); e != nil {
			return "", e
		}
		rows, err := tx.Query(ctx, `SELECT tenant_id FROM tenant_memberships WHERE principal_id=$1 ORDER BY tenant_id`, principal)
		if err != nil {
			return "", err
		}
		var tenants []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return "", err
			}
			tenants = append(tenants, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return "", err
		}
		for _, id := range tenants {
			if _, err = appendAuditTx(ctx, tx, id, auditInput(AuditActor{Type: "user", ID: principal}, "identity.password.reset", "principal", principal, json.RawMessage(`{}`))); err != nil {
				return "", err
			}
		}
	} else {
		return "", invalid("invalid token purpose")
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_tokens SET consumed_at=now() WHERE email=$1 AND purpose=$2 AND consumed_at IS NULL`, email, purpose); e != nil {
		return "", e
	}
	return tenant, tx.Commit(ctx)
}
func (s *Store) PasswordIdentity(ctx context.Context, tenant, email, password string) (auth.Identity, error) {
	email, e := NormalizeEmail(email)
	if e != nil {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if e != nil {
		return auth.Identity{}, e
	}
	defer tx.Rollback(ctx)
	var hash string
	actor := auth.Identity{TenantID: tenant, ActorType: "user"}
	e = tx.QueryRow(ctx, `SELECT principal_id,password_hash,updated_at::text FROM local_credentials WHERE email=$1`, email).Scan(&actor.ActorID, &hash, &actor.CredentialVersion)
	if e != nil {
		auth.VerifyPassword(dummyPasswordHash, password)
		return actor, auth.ErrUnauthenticated
	}
	if !auth.VerifyPassword(hash, password) {
		return actor, auth.ErrUnauthenticated
	}
	return currentActor(ctx, tx, actor)
}

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func (s *Store) AcceptPasswordInvitation(ctx context.Context, invite, email, password string) (string, error) {
	email, e := NormalizeEmail(email)
	if e != nil {
		return "", auth.ErrUnauthenticated
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var tenant string
	if e = tx.QueryRow(ctx, `SELECT tenant_id FROM team_invitations WHERE token_hash=$1`, tokenDigest(invite)).Scan(&tenant); e != nil {
		return "", auth.ErrUnauthenticated
	}
	if e = lockTeam(ctx, tx, tenant); e != nil {
		return "", e
	}
	i, _, e := validInvitation(ctx, tx, invite)
	if e != nil || i.Email != email {
		return "", auth.ErrUnauthenticated
	}
	var principal, hash string
	e = tx.QueryRow(ctx, `SELECT principal_id,password_hash FROM local_credentials WHERE email=$1 FOR SHARE`, email).Scan(&principal, &hash)
	if e != nil || !auth.VerifyPassword(hash, password) {
		return "", auth.ErrUnauthenticated
	}
	if e = bindInvitation(ctx, tx, i, tenant, principal); e != nil {
		return "", e
	}
	return tenant, tx.Commit(ctx)
}
func (s *Store) ChangeLocalPassword(ctx context.Context, actor auth.Identity, old, newPassword string) error {
	hash, e := auth.HashPassword(newPassword)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var expected string
	e = tx.QueryRow(ctx, `SELECT password_hash FROM local_credentials WHERE principal_id=$1 FOR UPDATE`, actor.ActorID).Scan(&expected)
	if e != nil || !auth.VerifyPassword(expected, old) {
		return auth.ErrUnauthenticated
	}
	if _, e = tx.Exec(ctx, `UPDATE local_credentials SET password_hash=$2,updated_at=now() WHERE principal_id=$1`, actor.ActorID, hash); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE browser_sessions SET revoked_at=now() WHERE principal_id=$1`, actor.ActorID); e != nil {
		return e
	}
	_, e = appendAuditTx(ctx, tx, actor.TenantID, auditInput(AuditActor{Type: actor.ActorType, ID: actor.ActorID}, "identity.password.change", "principal", actor.ActorID, json.RawMessage(`{}`)))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) AcceptOIDCInvitation(ctx context.Context, tenant, token string, claims auth.Claims) error {
	if !claims.EmailVerified {
		return auth.ErrUnauthenticated
	}
	email, e := NormalizeEmail(claims.Email)
	if e != nil {
		return auth.ErrUnauthenticated
	}
	tx, e := s.db.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockTeam(ctx, tx, tenant); e != nil {
		return e
	}
	i, invTenant, e := validInvitation(ctx, tx, token)
	if e != nil || invTenant != tenant || i.Email != email {
		return auth.ErrUnauthenticated
	}
	id := uuid.NewString()
	e = tx.QueryRow(ctx, `INSERT INTO principals(id,issuer,subject,email) VALUES($1,$2,$3,$4) ON CONFLICT(issuer,subject) DO UPDATE SET email=excluded.email RETURNING id`, id, claims.Issuer, claims.Subject, email).Scan(&id)
	if e != nil {
		return e
	}
	if e = bindInvitation(ctx, tx, i, tenant, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
