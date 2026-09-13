package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/auth"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type TeamRepository interface {
	TeamList(context.Context, auth.Identity, string) (any, error)
	TeamMutate(context.Context, auth.Identity, string, store.TeamCommand, store.TeamCipher) (json.RawMessage, error)
	PasswordIdentity(context.Context, string, string, string) (auth.Identity, error)
	IdentityRateLimit(context.Context, string, int, time.Duration) error
	RequestIdentityMail(context.Context, string, string, string, string, store.TeamCipher) error
	CompleteIdentityToken(context.Context, string, string, string) (string, error)
	AcceptPasswordInvitation(context.Context, string, string, string) (string, error)
	ChangeLocalPassword(context.Context, auth.Identity, string, string) error
}
type teamCipher struct{ codec *cookieCodec }

func (c teamCipher) Encode(v json.RawMessage) (string, error) {
	return c.codec.encode(v, "statusmon.identity.secrets.v1")
}
func (c teamCipher) Decode(v string) (json.RawMessage, error) {
	var out json.RawMessage
	e := c.codec.decode(v, "statusmon.identity.secrets.v1", &out)
	return out, e
}
func (s *Server) teamRoutes() {
	if _, ok := s.repository.(TeamRepository); !ok {
		return
	}
	for _, kind := range []string{"members", "invitations", "service-accounts"} {
		kind := kind
		s.mux.Handle("GET /v1/tenants/{tenant}/"+kind, s.authorize(auth.PermissionMemberWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			repo := s.repository.(TeamRepository)
			out, e := repo.TeamList(r.Context(), requestDetails(r).Identity, kind)
			if e != nil {
				writeProblem(w, r, e)
				return
			}
			writeJSON(w, 200, map[string]any{"data": out})
		})))
	}
	for route, action := range map[string]string{
		"PATCH /v1/tenants/{tenant}/members/{id}":                "member.update",
		"POST /v1/tenants/{tenant}/invitations":                  "invitation.create",
		"POST /v1/tenants/{tenant}/invitations/{id}/revoke":      "invitation.revoke",
		"POST /v1/tenants/{tenant}/service-accounts":             "service.create",
		"PATCH /v1/tenants/{tenant}/service-accounts/{id}":       "service.update",
		"POST /v1/tenants/{tenant}/service-accounts/{id}/rotate": "service.rotate",
	} {
		action := action
		s.mux.Handle(route, s.authorize(auth.PermissionMemberWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var c store.TeamCommand
			var raw json.RawMessage
			if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&raw); e != nil {
				writeProblemStatus(w, r, 400, "invalid_request", "Invalid request")
				return
			}
			if e := json.Unmarshal(raw, &c); e != nil {
				writeProblemStatus(w, r, 400, "invalid_request", "Invalid request")
				return
			}
			if action == "member.update" || action == "service.update" {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				if fields["role"] == nil || fields["enabled"] == nil || string(fields["enabled"]) == "null" {
					writeProblemStatus(w, r, 400, "invalid_request", "Both role and enabled are required")
					return
				}
			}
			c.Action = action
			c.ID = r.PathValue("id")
			out, e := s.repository.(TeamRepository).TeamMutate(r.Context(), requestDetails(r).Identity, r.Header.Get("Idempotency-Key"), c, teamCipher{s.sessions.codec})
			if e != nil {
				writeProblem(w, r, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, out)
		})))
	}
	s.mux.HandleFunc("POST /auth/team/{action}", s.handleTeamAuth)
	s.mux.Handle("POST /v1/tenants/{tenant}/account/{action}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleAccountAction)))
}
func (s *Server) handleAccountAction(w http.ResponseWriter, r *http.Request) {
	actor := requestDetails(r).Identity
	if actor.ActorType != "user" {
		writeProblem(w, r, auth.ErrForbidden)
		return
	}
	var e error
	switch r.PathValue("action") {
	case "password":
		var body struct {
			Old      string `json:"old_password"`
			Password string `json:"password"`
		}
		if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); e == nil {
			if utf8.RuneCountInString(body.Password) < 12 || utf8.RuneCountInString(body.Password) > 128 {
				writeProblemStatus(w, r, 400, "invalid_password", "Password must contain 12–128 characters")
				return
			}
			if e = s.repository.(TeamRepository).IdentityRateLimit(r.Context(), "change-password:"+actor.ActorID, 10, 15*time.Minute); e != nil {
				writeProblemStatus(w, r, 429, "rate_limited", "Too many attempts. Try again later.")
				return
			}
			e = s.repository.(TeamRepository).ChangeLocalPassword(r.Context(), actor, body.Old, body.Password)
		}
	case "logout-all":
		e = s.sessions.Store.RevokeBrowserSessions(r.Context(), actor, "")
	default:
		writeProblemStatus(w, r, 404, "not_found", "Unknown account action")
		return
	}
	if e != nil {
		writeProblem(w, r, e)
		return
	}
	s.sessions.Clear(w)
	writeJSON(w, 200, map[string]bool{"completed": true})
}
func (s *Server) handleTeamAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// JSON plus same-origin checks prevent login CSRF without a pre-login session.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeProblemStatus(w, r, 415, "json_required", "JSON is required")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && (s.oidc == nil || origin != s.oidc.publicURL.Scheme+"://"+s.oidc.publicURL.Host) {
		writeProblem(w, r, auth.ErrForbidden)
		return
	}
	var body struct {
		Tenant     string `json:"tenant"`
		Email      string `json:"email"`
		Password   string `json:"password"`
		Token      string `json:"token"`
		Invitation string `json:"invitation"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); e != nil {
		writeProblemStatus(w, r, 400, "invalid_request", "Invalid request")
		return
	}
	repo := s.repository.(TeamRepository)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	action := r.PathValue("action")
	email := strings.ToLower(strings.TrimSpace(body.Email))
	for _, key := range []string{"ip:" + ip, "email:" + email + ":" + action} {
		if e := repo.IdentityRateLimit(r.Context(), key, 20, 15*time.Minute); e != nil {
			writeProblemStatus(w, r, 429, "rate_limited", "Too many attempts. Try again later.")
			return
		}
	}
	var e error
	switch action {
	case "login":
		var tenant store.Tenant
		tenant, e = s.repository.ResolveTenant(r.Context(), body.Tenant)
		if e != nil {
			e = auth.ErrUnauthenticated
			break
		}
		var actor auth.Identity
		actor, e = repo.PasswordIdentity(r.Context(), tenant.ID, email, body.Password)
		if e != nil {
			break
		}
		var session Session
		session, e = s.sessions.NewSession(TenantContext{ID: tenant.ID, Key: body.Tenant, Slug: tenant.Slug, Name: tenant.Name}, actor)
		if e == nil {
			e = s.sessions.Set(w, session)
		}
	case "verify-request", "forgot":
		purpose := "verify"
		if action == "forgot" {
			purpose = "reset"
		}
		if s.oidc == nil {
			e = errors.New("public URL unavailable")
			break
		}
		if e = repo.IdentityRateLimit(r.Context(), "mail:"+email, 1, time.Minute); e != nil {
			writeJSON(w, 200, map[string]bool{"completed": true})
			return
		}
		e = repo.RequestIdentityMail(r.Context(), purpose, email, body.Invitation, s.oidc.publicURL.String(), teamCipher{s.sessions.codec})
		if action == "forgot" && e != nil {
			writeProblemStatus(w, r, 503, "mail_unavailable", "Unable to queue email")
			return
		}
	case "verify", "reset":
		if utf8.RuneCountInString(body.Password) < 12 || utf8.RuneCountInString(body.Password) > 128 {
			writeProblemStatus(w, r, 400, "invalid_password", "Password must contain 12–128 characters")
			return
		}
		var tenant string
		tenant, e = repo.CompleteIdentityToken(r.Context(), body.Token, action, body.Password)
		if e == nil {
			writeJSON(w, 200, map[string]any{"completed": true, "tenant": tenant})
			return
		}
	case "accept":
		var tenant string
		tenant, e = repo.AcceptPasswordInvitation(r.Context(), body.Invitation, email, body.Password)
		if e == nil {
			writeJSON(w, 200, map[string]any{"completed": true, "tenant": tenant})
			return
		}
	default:
		writeProblemStatus(w, r, 404, "not_found", "Unknown authentication action")
		return
	}
	if e != nil {
		writeProblem(w, r, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"completed": true})
}
