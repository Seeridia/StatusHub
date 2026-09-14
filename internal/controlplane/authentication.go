package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Seeridia/StatusHub/internal/auth"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

type AccountRepository interface {
	PasswordUser(context.Context, string, string) (store.User, error)
	UserWorkspaces(context.Context, string) ([]store.Workspace, error)
	IdentityRateLimit(context.Context, string, int, time.Duration) error
	CompleteSetup(context.Context, string, string, string, string, string) (store.User, error)
	PreviewInvitation(context.Context, string) (store.InvitationPreview, error)
	AcceptInvitation(context.Context, string, string, string, string) (store.User, error)
	RequestUserMail(context.Context, string, string, string, store.TeamCipher) error
	CompleteUserToken(context.Context, string, string, string) error
	ChangeUserPassword(context.Context, string, string, string) error
}

func publicOrigin(v string) (string, error) {
	u, e := url.Parse(v)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return "", errors.New("invalid public origin")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return "", errors.New("HTTPS public origin required")
	}
	return u.Scheme + "://" + u.Host, nil
}
func smtpConfigured() bool {
	return os.Getenv("STATUSHUB_SMTP_ADDRESS") != "" && os.Getenv("STATUSHUB_SMTP_FROM") != ""
}
func (s *Server) authRoutes() {
	for _, p := range []string{"login", "logout", "logout-all", "password/forgot", "password/reset", "password/change", "setup", "invitations/preview", "invitations/accept", "email/request", "email/verify"} {
		s.mux.HandleFunc("POST /auth/"+p, s.handleAuth)
	}
	s.mux.HandleFunc("GET /auth/session", s.handleAuthSession)
}
func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	b, e := s.sessions.Read(r)
	if e != nil {
		s.authError(w, r, "session", e)
		return
	}
	workspaces, e := s.repository.(AccountRepository).UserWorkspaces(r.Context(), b.User.ID)
	if e != nil {
		s.authError(w, r, "session", e)
		return
	}
	writeJSON(w, 200, map[string]any{"user": b.User, "workspaces": workspaces, "csrf_token": b.CSRF, "mail_configured": smtpConfigured()})
}
func (s *Server) authIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip, e := netip.ParseAddr(host)
	if e != nil {
		return "unknown"
	}
	trusted := func(a netip.Addr) bool {
		for _, p := range s.config.TrustedProxies {
			if p.Contains(a.Unmap()) {
				return true
			}
		}
		return false
	}
	if !trusted(ip) {
		return ip.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(chain) - 1; i >= 0; i-- {
		a, e := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if e != nil {
			return ip.String()
		}
		ip = a
		if !trusted(ip) {
			break
		}
	}
	return ip.String()
}
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(r.URL.Path, "/auth/")
	origin, _ := publicOrigin(s.config.PublicURL)
	if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
		s.authProblem(w, r, action, 403, "origin_mismatch", "The service public URL does not match this page")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		s.authProblem(w, r, action, 415, "json_required", "JSON is required")
		return
	}
	// Native clients may omit Origin; cross-site browser form posts cannot use this content type.
	var b struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		OldPassword string `json:"old_password"`
		Token       string `json:"token"`
		Name        string `json:"name"`
		Slug        string `json:"slug"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if e := d.Decode(&b); e != nil {
		s.authProblem(w, r, action, 400, "invalid_request", "Invalid request fields")
		return
	}
	if e := d.Decode(&struct{}{}); e != io.EOF {
		s.authProblem(w, r, action, 400, "invalid_request", "Invalid JSON")
		return
	}
	repo := s.repository.(AccountRepository)
	for _, key := range []string{"ip:" + s.authIP(r) + ":" + action, "email:" + strings.ToLower(strings.TrimSpace(b.Email)) + ":" + action + ":" + s.authIP(r)} {
		if e := repo.IdentityRateLimit(r.Context(), key, 30, 15*time.Minute); e != nil {
			s.authError(w, r, action, e)
			return
		}
	}
	var user store.User
	var e error
	requireSession := func() (store.BrowserSession, bool) {
		session, err := s.sessions.Read(r)
		if err != nil {
			s.authError(w, r, action, err)
			return session, false
		}
		if !constantTimeEqual(r.Header.Get("X-CSRF-Token"), session.CSRF) {
			s.authProblem(w, r, action, 403, "csrf_failed", "Refresh this page and try again")
			return session, false
		}
		return session, true
	}
	switch action {
	case "login":
		user, e = repo.PasswordUser(r.Context(), b.Email, b.Password)
		if e == nil {
			e = s.sessions.Set(r.Context(), w, user)
		}
	case "setup":
		user, e = repo.CompleteSetup(r.Context(), b.Token, b.Email, b.Password, b.Name, b.Slug)
		if e == nil {
			e = s.sessions.Set(r.Context(), w, user)
		}
	case "invitations/preview":
		var preview store.InvitationPreview
		preview, e = repo.PreviewInvitation(r.Context(), b.Token)
		if e == nil {
			writeJSON(w, 200, preview)
			return
		}
	case "invitations/accept":
		uid, sessionToken := "", ""
		if session, err := s.sessions.Read(r); err == nil {
			if !constantTimeEqual(r.Header.Get("X-CSRF-Token"), session.CSRF) {
				s.authProblem(w, r, action, 403, "csrf_failed", "Refresh this page and try again")
				return
			}
			uid = session.User.ID
			cookie, _ := r.Cookie(sessionCookieName)
			sessionToken = cookie.Value
		}
		user, e = repo.AcceptInvitation(r.Context(), b.Token, b.Password, uid, sessionToken)
		if e == nil {
			e = s.sessions.Set(r.Context(), w, user)
		}
	case "password/forgot", "email/request":
		email := b.Email
		purpose := "reset"
		if action == "email/request" {
			session, ok := requireSession()
			if !ok {
				return
			}
			email = session.User.Email
			purpose = "verify-email"
		}
		if !smtpConfigured() {
			s.authProblem(w, r, action, 503, "mail_unavailable", "Email service is not configured")
			return
		}
		if err := repo.IdentityRateLimit(r.Context(), "mail:"+strings.ToLower(strings.TrimSpace(email)), 1, time.Minute); err != nil {
			if errors.Is(err, store.ErrRateLimited) {
				writeJSON(w, 200, map[string]bool{"completed": true})
				return
			}
			s.authError(w, r, action, err)
			return
		}
		e = repo.RequestUserMail(r.Context(), purpose, email, origin, teamCipher{s.sessions.codec})
	case "password/reset", "email/verify":
		purpose := "reset"
		if action == "email/verify" {
			purpose = "verify-email"
		}
		e = repo.CompleteUserToken(r.Context(), b.Token, purpose, b.Password)
		if e == nil && purpose == "reset" {
			s.sessions.Clear(w)
		}
	case "password/change", "logout", "logout-all":
		session, ok := requireSession()
		if !ok {
			return
		}
		if action == "password/change" {
			e = repo.ChangeUserPassword(r.Context(), session.User.ID, b.OldPassword, b.Password)
		} else {
			token := ""
			if action == "logout" {
				c, _ := r.Cookie(sessionCookieName)
				token = c.Value
			}
			e = s.sessions.Store.RevokeUserSessions(r.Context(), session.User.ID, token)
		}
		if e == nil {
			s.sessions.Clear(w)
		}
	default:
		s.authProblem(w, r, action, 404, "not_found", "Unknown action")
		return
	}
	if e != nil {
		s.authError(w, r, action, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"completed": true})
}
func (s *Server) authProblem(w http.ResponseWriter, r *http.Request, stage string, status int, code, detail string) {
	s.config.Logger.Warn("authentication action failed", "stage", stage, "code", code, "request_id", requestID(r))
	writeProblemStatus(w, r, status, code, detail)
}
func (s *Server) authError(w http.ResponseWriter, r *http.Request, stage string, e error) {
	status, code, detail := 500, "auth_unavailable", "Authentication service unavailable"
	switch {
	case errors.Is(e, store.ErrRateLimited):
		status, code, detail = 429, "rate_limited", "Too many attempts. Try again later"
	case errors.Is(e, store.ErrInvitationInvalid):
		status, code, detail = 410, "invitation_invalid", "Invitation expired, revoked or already accepted"
	case errors.Is(e, store.ErrSetupUnavailable):
		status, code, detail = 409, "setup_unavailable", "Setup link expired or instance already initialized"
	case errors.Is(e, store.ErrPasswordInvalid):
		status, code, detail = 400, "invalid_password", "Password must contain 12–128 characters"
	case errors.Is(e, store.ErrLoginRequired):
		status, code, detail = 401, "login_required", "Sign in to accept this invitation"
	case errors.Is(e, store.ErrEmailMismatch):
		status, code, detail = 403, "email_mismatch", "Sign in with the invited email"
	case errors.Is(e, store.ErrConflict):
		status, code, detail = 409, "membership_exists", "Membership already exists"
	case errors.Is(e, store.ErrInvalidArgument):
		status, code, detail = 400, "invalid_request", "Check the form fields"
	case errors.Is(e, auth.ErrUnauthenticated):
		status, code, detail = 401, "session_expired", "Session expired. Sign in again"
		if stage == "login" || stage == "password/change" {
			code, detail = "invalid_credentials", "Email or password is incorrect"
		}
		if stage == "password/reset" || stage == "email/verify" {
			code, detail = "token_invalid", "Link expired or already used"
		}
	case errors.Is(e, auth.ErrForbidden):
		status, code, detail = 403, "workspace_forbidden", "Workspace access denied"
	}
	s.authProblem(w, r, stage, status, code, detail)
}
