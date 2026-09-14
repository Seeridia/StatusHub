package controlplane

import (
	"context"
	"errors"
	"github.com/Seeridia/StatusHub/internal/auth"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
	"net/http"
	"time"
)

const sessionCookieName = "statushub_session"

type TenantContext struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}
type SessionStore interface {
	CreateUserSession(context.Context, string, string, store.User, time.Time) error
	UserSession(context.Context, string) (store.BrowserSession, error)
	RevokeUserSessions(context.Context, string, string) error
	MemberIdentity(context.Context, string, string) (auth.Identity, error)
}
type SessionManager struct {
	Store         SessionStore
	codec         *cookieCodec
	secureCookies bool
	sessionTTL    time.Duration
}

func NewSessionManager(master []byte, secure bool, ttl time.Duration) (*SessionManager, error) {
	codec, e := newCookieCodec(master)
	if e != nil {
		return nil, e
	}
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return nil, errors.New("invalid session TTL")
	}
	return &SessionManager{codec: codec, secureCookies: secure, sessionTTL: ttl}, nil
}
func (m *SessionManager) Set(ctx context.Context, w http.ResponseWriter, user store.User) error {
	token, e := randomToken(32)
	if e != nil {
		return e
	}
	csrf, e := randomToken(32)
	if e != nil {
		return e
	}
	expires := time.Now().Add(m.sessionTTL)
	if e = m.Store.CreateUserSession(ctx, token, csrf, user, expires); e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(m.sessionTTL.Seconds()), Expires: expires})
	return nil
}
func (m *SessionManager) Read(r *http.Request) (store.BrowserSession, error) {
	c, e := r.Cookie(sessionCookieName)
	if e != nil || len(c.Value) != 43 {
		return store.BrowserSession{}, auth.ErrUnauthenticated
	}
	return m.Store.UserSession(r.Context(), c.Value)
}
func (m *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
func (s *Server) checkStreamSession(r *http.Request, tenant string) (auth.Identity, error) {
	session, e := s.sessions.Read(r)
	if e != nil {
		return auth.Identity{}, e
	}
	return s.sessions.Store.MemberIdentity(r.Context(), session.User.ID, tenant)
}
