package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
	"time"
)

type testTeamCipher struct{}

func (testTeamCipher) Encode(v json.RawMessage) (string, error) { return "encrypted:" + string(v), nil }
func (testTeamCipher) Decode(v string) (json.RawMessage, error) {
	return json.RawMessage(strings.TrimPrefix(v, "encrypted:")), nil
}
func TestIntegrationTeamLifecycle(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	_, e := db.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'team','Team')`, tenant)
	if e != nil {
		t.Fatal(e)
	}
	invite, e := db.store.BootstrapInvitation(ctx, tenant, "owner@example.test")
	if e != nil {
		t.Fatal(e)
	}
	cipher := testTeamCipher{}
	if e = db.store.RequestIdentityMail(ctx, "verify", "owner@example.test", invite, "http://localhost", cipher); e != nil {
		t.Fatal(e)
	}
	var payload string
	if e = db.pool.QueryRow(ctx, `SELECT payload FROM identity_mail_jobs`).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	var mail map[string]string
	raw, _ := cipher.Decode(payload)
	json.Unmarshal(raw, &mail)
	token := strings.Split(mail["url"], "&token=")[1]
	if _, e = db.store.CompleteIdentityToken(ctx, token, "verify", "a strong initial password"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.store.CompleteIdentityToken(ctx, token, "verify", "a strong initial password"); e == nil {
		t.Fatal("verification token replay")
	}
	owner, e := db.store.PasswordIdentity(ctx, tenant, "OWNER@example.test", "a strong initial password")
	if e != nil || owner.Role != auth.RoleOwner {
		t.Fatalf("owner login: %+v %v", owner, e)
	}
	if _, e = db.store.BootstrapInvitation(ctx, tenant, "other@example.test"); !errors.Is(e, ErrConflict) {
		t.Fatal("bootstrap replaced owner")
	}
	if e = db.store.CreateBrowserSession(ctx, "session", owner, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	mutate := func(actor auth.Identity, key string, c TeamCommand) map[string]any {
		t.Helper()
		b, e := db.store.TeamMutate(ctx, actor, key, c, cipher)
		if e != nil {
			t.Fatal(e)
		}
		var out map[string]any
		json.Unmarshal(b, &out)
		return out
	}
	service := mutate(owner, "service", TeamCommand{Action: "service.create", Name: "operator bot", Role: auth.RoleOperator})
	secret := service["token"].(string)
	id := service["id"].(string)
	again := mutate(owner, "service", TeamCommand{Action: "service.create", Name: "operator bot", Role: auth.RoleOperator})
	if again["token"] != secret {
		t.Fatal("idempotency token changed")
	}
	if _, e = db.store.AuthenticateServiceAccount(ctx, tenant, secret); e != nil {
		t.Fatal(e)
	}
	rotated := mutate(owner, "rotate", TeamCommand{Action: "service.rotate", ID: id})
	if _, e = db.store.AuthenticateServiceAccount(ctx, tenant, secret); e == nil {
		t.Fatal("old token accepted")
	}
	if _, e = db.store.AuthenticateServiceAccount(ctx, tenant, rotated["token"].(string)); e != nil {
		t.Fatal(e)
	}
	mutate(owner, "disable", TeamCommand{Action: "service.update", ID: id, Role: auth.RoleOperator, Enabled: false})
	if _, e = db.store.AuthenticateServiceAccount(ctx, tenant, rotated["token"].(string)); e == nil {
		t.Fatal("disabled service accepted")
	}
	if _, e = db.pool.Exec(ctx, `UPDATE identity_mutations SET secret_until=now()-interval '1 second' WHERE key='service'`); e != nil {
		t.Fatal(e)
	}
	if mutate(owner, "service", TeamCommand{Action: "service.create", Name: "operator bot", Role: auth.RoleOperator})["secret_unavailable"] != true {
		t.Fatal("expired secret returned")
	}
	if e = db.store.RequestIdentityMail(ctx, "reset", "owner@example.test", "", "http://localhost", cipher); e != nil {
		t.Fatal(e)
	}
	if e = db.pool.QueryRow(ctx, `SELECT payload FROM identity_mail_jobs ORDER BY created_at DESC LIMIT 1`).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	raw, _ = cipher.Decode(payload)
	json.Unmarshal(raw, &mail)
	token = strings.Split(mail["url"], "&token=")[1]
	if _, e = db.store.CompleteIdentityToken(ctx, token, "reset", "a new stronger password"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.store.ResolveBrowserSession(ctx, "session", tenant); e == nil {
		t.Fatal("reset preserved session")
	}
	if _, e = db.store.PasswordIdentity(ctx, tenant, "owner@example.test", "a strong initial password"); e == nil {
		t.Fatal("old password accepted")
	}
	if _, e = db.store.PasswordIdentity(ctx, tenant, "owner@example.test", "a new stronger password"); e != nil {
		t.Fatal(e)
	}
}
func TestIntegrationTeamAuthorityAndConcurrency(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	_, e := db.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'authority','Authority')`, tenant)
	if e != nil {
		t.Fatal(e)
	}
	makeUser := func(role auth.Role) auth.Identity {
		t.Helper()
		id, e := db.store.SetTenantMembership(ctx, SetTenantMembershipParams{TenantID: tenant, Issuer: "https://idp.example", Subject: uuid.NewString(), Role: role})
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	a, b, admin := makeUser(auth.RoleOwner), makeUser(auth.RoleOwner), makeUser(auth.RoleAdmin)
	cipher := testTeamCipher{}
	if _, e = db.store.TeamMutate(ctx, admin, "denied", TeamCommand{Action: "member.update", ID: a.ActorID, Role: auth.RoleViewer, Enabled: true}, cipher); !errors.Is(e, auth.ErrForbidden) {
		t.Fatal("admin managed owner")
	}
	if _, e = db.store.TeamMutate(ctx, a, "self", TeamCommand{Action: "member.update", ID: a.ActorID, Role: auth.RoleViewer, Enabled: true}, cipher); !errors.Is(e, auth.ErrForbidden) {
		t.Fatal("self change allowed")
	}
	// Two owner service accounts act concurrently on distinct human owners.
	sa, e := db.store.CreateServiceAccount(ctx, CreateServiceAccountParams{TenantID: tenant, Name: "bootstrap", Role: auth.RoleOwner})
	if e != nil {
		t.Fatal(e)
	}
	actor, e := db.store.AuthenticateServiceAccount(ctx, tenant, sa.Token)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{a.ActorID, b.ActorID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := db.store.TeamMutate(ctx, actor, id, TeamCommand{Action: "member.update", ID: id, Role: auth.RoleViewer, Enabled: true}, cipher)
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("owner guard successes %d", success)
	}
	// A pending invite must not survive loss of the inviter's authority.
	out, e := db.store.TeamMutate(ctx, admin, "invite", TeamCommand{Action: "invitation.create", Email: "new@example.test", Role: auth.RoleViewer}, cipher)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]string
	json.Unmarshal(out, &v)
	if _, e = db.store.TeamMutate(ctx, actor, "demote", TeamCommand{Action: "member.update", ID: admin.ActorID, Role: auth.RoleViewer, Enabled: true}, cipher); e != nil {
		t.Fatal(e)
	}
	if e = db.store.RequestIdentityMail(ctx, "verify", "new@example.test", v["invitation_token"], "http://localhost", cipher); e == nil {
		t.Fatal("demoted inviter still authorized")
	}
	other := actor
	other.TenantID = uuid.NewString()
	if _, e = db.store.TeamList(ctx, other, "members"); e == nil {
		t.Fatal("cross tenant accepted")
	}
}

func TestIntegrationTeamSSOAndRevocation(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	if _, e := db.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'sso-team','SSO team')`, tenant); e != nil {
		t.Fatal(e)
	}
	sa, e := db.store.CreateServiceAccount(ctx, CreateServiceAccountParams{TenantID: tenant, Name: "owner", Role: auth.RoleOwner})
	if e != nil {
		t.Fatal(e)
	}
	owner, e := db.store.AuthenticateServiceAccount(ctx, tenant, sa.Token)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := db.store.TeamMutate(ctx, owner, "sso-invite", TeamCommand{Action: "invitation.create", Email: "member@example.test", Role: auth.RoleOperator}, testTeamCipher{})
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]string
	json.Unmarshal(raw, &out)
	claims := auth.Claims{Issuer: "https://idp.example", Subject: "stable-id", Email: "member@example.test", EmailVerified: false}
	if e = db.store.AcceptOIDCInvitation(ctx, tenant, out["invitation_token"], claims); e == nil {
		t.Fatal("unverified email accepted")
	}
	claims.EmailVerified = true
	claims.Email = "wrong@example.test"
	if e = db.store.AcceptOIDCInvitation(ctx, tenant, out["invitation_token"], claims); e == nil {
		t.Fatal("wrong email accepted")
	}
	claims.Email = "member@example.test"
	if e = db.store.AcceptOIDCInvitation(ctx, tenant, out["invitation_token"], claims); e != nil {
		t.Fatal(e)
	}
	if e = db.store.AcceptOIDCInvitation(ctx, tenant, out["invitation_token"], claims); e == nil {
		t.Fatal("replayed SSO invite")
	}
	member, e := db.store.ResolveOIDCIdentity(ctx, tenant, claims)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.store.CreateBrowserSession(ctx, "sso-session", member, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.store.TeamMutate(ctx, owner, "lower", TeamCommand{Action: "member.update", ID: member.ActorID, Role: auth.RoleViewer, Enabled: true}, testTeamCipher{}); e != nil {
		t.Fatal(e)
	}
	latest, e := db.store.ResolveBrowserSession(ctx, "sso-session", tenant)
	if e != nil || latest.Role != auth.RoleViewer {
		t.Fatal("session retained old role")
	}
	if _, e = db.store.TeamMutate(ctx, owner, "stop", TeamCommand{Action: "member.update", ID: member.ActorID, Role: auth.RoleViewer, Enabled: false}, testTeamCipher{}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.store.ResolveBrowserSession(ctx, "sso-session", tenant); e == nil {
		t.Fatal("disabled session survived")
	}
	if _, e = db.store.ResolveOIDCIdentity(ctx, tenant, claims); e == nil {
		t.Fatal("disabled SSO membership authenticated")
	}
	if e = db.store.IdentityRateLimit(ctx, "test", 1, time.Minute); e != nil {
		t.Fatal(e)
	}
	if !errors.Is(db.store.IdentityRateLimit(ctx, "test", 1, time.Minute), ErrRateLimited) {
		t.Fatal("rate limit missing")
	}
}
