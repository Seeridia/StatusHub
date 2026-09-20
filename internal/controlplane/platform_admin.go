package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type PlatformRepository interface {
	IsPlatformAdmin(context.Context, string) (bool, error)
	PlatformOverview(context.Context) (store.PlatformSummary, error)
	ListPlatformUsers(context.Context, string, int) ([]store.PlatformUser, error)
	ListPlatformWorkspaces(context.Context, string, int) ([]store.PlatformWorkspace, error)
	ListPlatformAuditEvents(context.Context, string, int) ([]store.PlatformAuditEvent, error)
	SetPlatformAdmin(context.Context, string, string, bool) error
	CreatePlatformWorkspace(context.Context, string, string, string, string) (store.PlatformWorkspace, error)
	SetPlatformUserEnabled(context.Context, string, string, bool) error
	RevokePlatformUserSessions(context.Context, string, string) error
}

type platformRequestContext struct {
	User      store.User
	RequestID string
}

type platformContextKey struct{}

func (s *Server) platformRoutes() {
	if _, ok := s.repository.(PlatformRepository); !ok {
		return
	}
	s.mux.Handle("GET /admin/v1/summary", s.authorizePlatform(http.HandlerFunc(s.handlePlatformSummary)))
	s.mux.Handle("GET /admin/v1/users", s.authorizePlatform(http.HandlerFunc(s.handlePlatformUsers)))
	s.mux.Handle("PATCH /admin/v1/users/{id}", s.authorizePlatform(http.HandlerFunc(s.handlePlatformUserUpdate)))
	s.mux.Handle("POST /admin/v1/users/{id}/revoke-sessions", s.authorizePlatform(http.HandlerFunc(s.handlePlatformUserSessions)))
	s.mux.Handle("GET /admin/v1/workspaces", s.authorizePlatform(http.HandlerFunc(s.handlePlatformWorkspaces)))
	s.mux.Handle("POST /admin/v1/workspaces", s.authorizePlatform(http.HandlerFunc(s.handlePlatformWorkspaceCreate)))
	s.mux.Handle("POST /admin/v1/platform-admins", s.authorizePlatform(http.HandlerFunc(s.handlePlatformAdminGrant)))
	s.mux.Handle("DELETE /admin/v1/platform-admins/{id}", s.authorizePlatform(http.HandlerFunc(s.handlePlatformAdminRevoke)))
	s.mux.Handle("GET /admin/v1/audit-events", s.authorizePlatform(http.HandlerFunc(s.handlePlatformAuditEvents)))
}

func (s *Server) authorizePlatform(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.sessions.Read(r)
		if err != nil {
			writeProblemStatus(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required")
			return
		}
		allowed, err := s.repository.(PlatformRepository).IsPlatformAdmin(r.Context(), session.User.ID)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
		if !allowed {
			writeProblemStatus(w, r, http.StatusForbidden, "platform_forbidden", "Platform administrator access is required")
			return
		}
		if isUnsafeMethod(r.Method) && !constantTimeEqual(r.Header.Get("X-CSRF-Token"), session.CSRF) {
			writeProblemStatus(w, r, http.StatusForbidden, "csrf_failed", "CSRF token is missing or invalid")
			return
		}
		ctx := platformRequestContext{User: session.User, RequestID: requestID(r)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), platformContextKey{}, ctx)))
	})
}

func platformDetails(r *http.Request) platformRequestContext {
	v, _ := r.Context().Value(platformContextKey{}).(platformRequestContext)
	return v
}

func platformLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n < 1 || n > 500 {
		return 100
	}
	return n
}

func (s *Server) handlePlatformSummary(w http.ResponseWriter, r *http.Request) {
	out, err := s.repository.(PlatformRepository).PlatformOverview(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePlatformUsers(w http.ResponseWriter, r *http.Request) {
	out, err := s.repository.(PlatformRepository).ListPlatformUsers(r.Context(), r.URL.Query().Get("query"), platformLimit(r))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePlatformWorkspaces(w http.ResponseWriter, r *http.Request) {
	out, err := s.repository.(PlatformRepository).ListPlatformWorkspaces(r.Context(), r.URL.Query().Get("query"), platformLimit(r))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePlatformWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name       string `json:"name"`
		Slug       string `json:"slug"`
		AdminEmail string `json:"admin_email"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeProblemStatus(w, r, http.StatusBadRequest, "invalid_request", "Workspace name, slug, and administrator email are required")
		return
	}
	out, err := s.repository.(PlatformRepository).CreatePlatformWorkspace(r.Context(), platformDetails(r).User.ID, input.AdminEmail, input.Name, input.Slug)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handlePlatformAdminGrant(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"user_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.UserID) == "" {
		writeProblemStatus(w, r, http.StatusBadRequest, "invalid_request", "User ID is required")
		return
	}
	if err := s.repository.(PlatformRepository).SetPlatformAdmin(r.Context(), platformDetails(r).User.ID, strings.TrimSpace(input.UserID), true); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": true})
}

func (s *Server) handlePlatformAdminRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.repository.(PlatformRepository).SetPlatformAdmin(r.Context(), platformDetails(r).User.ID, strings.TrimSpace(r.PathValue("id")), false); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": true})
}

func (s *Server) handlePlatformAuditEvents(w http.ResponseWriter, r *http.Request) {
	out, err := s.repository.(PlatformRepository).ListPlatformAuditEvents(r.Context(), r.URL.Query().Get("query"), platformLimit(r))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) handlePlatformUserUpdate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.Enabled == nil {
		writeProblemStatus(w, r, http.StatusBadRequest, "invalid_request", "Enabled is required")
		return
	}
	actor := platformDetails(r).User.ID
	if err := s.repository.(PlatformRepository).SetPlatformUserEnabled(r.Context(), actor, strings.TrimSpace(r.PathValue("id")), *input.Enabled); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": true})
}

func (s *Server) handlePlatformUserSessions(w http.ResponseWriter, r *http.Request) {
	actor := platformDetails(r).User.ID
	if err := s.repository.(PlatformRepository).RevokePlatformUserSessions(r.Context(), actor, strings.TrimSpace(r.PathValue("id"))); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"completed": true})
}
