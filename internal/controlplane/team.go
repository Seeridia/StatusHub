package controlplane

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Seeridia/StatusHub/internal/auth"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type TeamRepository interface {
	TeamList(context.Context, auth.Identity, string) (any, error)
	TeamMutate(context.Context, auth.Identity, string, store.TeamCommand, store.TeamCipher) (json.RawMessage, error)
}
type teamCipher struct{ codec *cookieCodec }

func (c teamCipher) Encode(v json.RawMessage) (string, error) {
	return c.codec.encode(v, "statushub.identity.secrets.v1")
}
func (c teamCipher) Decode(v string) (json.RawMessage, error) {
	var out json.RawMessage
	e := c.codec.decode(v, "statushub.identity.secrets.v1", &out)
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
		"POST /v1/tenants/{tenant}/members/admin-transfer":       "admin.transfer",
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
			if action == "invitation.create" && !smtpConfigured() {
				writeProblemStatus(w, r, 503, "mail_unavailable", "Configure SMTP before sending invitations")
				return
			}
			c.PublicURL = s.config.PublicURL
			c.Action = action
			if pathID := r.PathValue("id"); pathID != "" {
				c.ID = pathID
			}
			out, e := s.repository.(TeamRepository).TeamMutate(r.Context(), requestDetails(r).Identity, r.Header.Get("Idempotency-Key"), c, teamCipher{s.sessions.codec})
			if e != nil {
				writeProblem(w, r, e)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, out)
		})))
	}
	s.authRoutes()
}
