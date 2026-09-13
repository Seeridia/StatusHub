package controlplane

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/auth"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

const (
	sessionCookieName = "statusmon_session"
	loginCookieName   = "statusmon_oidc_login"
	maximumCookieSize = 16 << 10
)

type Session struct {
	ID        string        `json:"id"`
	Version   int           `json:"v"`
	TenantID  string        `json:"tenant_id"`
	TenantKey string        `json:"tenant_key"`
	Identity  auth.Identity `json:"identity"`
	CSRF      string        `json:"csrf"`
	ExpiresAt time.Time     `json:"expires_at"`
}

type loginState struct {
	Invitation   string    `json:"invitation,omitempty"`
	Version      int       `json:"v"`
	TenantID     string    `json:"tenant_id"`
	TenantKey    string    `json:"tenant_key"`
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	State        string    `json:"state"`
	Nonce        string    `json:"nonce"`
	PKCEVerifier string    `json:"pkce_verifier"`
	TokenURL     string    `json:"token_url"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type cookieCodec struct {
	aead cipher.AEAD
}

func newCookieCodec(masterKey []byte) (*cookieCodec, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("controlplane: session master key must be 32 bytes")
	}
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("statusmon.browser-session.v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &cookieCodec{aead: aead}, nil
}

func (c *cookieCodec) encode(value any, purpose string) (string, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, []byte(purpose))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *cookieCodec) decode(value, purpose string, target any) error {
	if c == nil || len(value) == 0 || len(value) > maximumCookieSize {
		return auth.ErrUnauthenticated
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) < c.aead.NonceSize()+c.aead.Overhead() {
		return auth.ErrUnauthenticated
	}
	nonce := sealed[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, sealed[c.aead.NonceSize():], []byte(purpose))
	if err != nil {
		return auth.ErrUnauthenticated
	}
	defer clear(plaintext)
	decoder := json.NewDecoder(strings.NewReader(string(plaintext)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return auth.ErrUnauthenticated
	}
	return nil
}

type SessionManager struct {
	Store interface {
		CreateBrowserSession(context.Context, string, auth.Identity, time.Time) error
		ResolveBrowserSession(context.Context, string, string) (auth.Identity, error)
		RevokeBrowserSessions(context.Context, auth.Identity, string) error
	}
	codec         *cookieCodec
	secureCookies bool
	sessionTTL    time.Duration
	now           func() time.Time
}

func NewSessionManager(masterKey []byte, secureCookies bool, sessionTTL time.Duration) (*SessionManager, error) {
	codec, err := newCookieCodec(masterKey)
	if err != nil {
		return nil, err
	}
	if sessionTTL <= 0 || sessionTTL > 24*time.Hour {
		return nil, errors.New("controlplane: session TTL must be in (0,24h]")
	}
	return &SessionManager{codec: codec, secureCookies: secureCookies, sessionTTL: sessionTTL,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (m *SessionManager) NewSession(tenant TenantContext, identity auth.Identity) (Session, error) {
	csrf, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	id, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, Version: 2, TenantID: tenant.ID, TenantKey: tenant.Key, Identity: identity,
		CSRF: csrf, ExpiresAt: m.now().Add(m.sessionTTL)}, nil
}

func (m *SessionManager) Set(response http.ResponseWriter, session Session) error {
	if m.Store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.Store.CreateBrowserSession(ctx, session.ID, session.Identity, session.ExpiresAt); err != nil {
			return err
		}
	}
	encoded, err := m.codec.encode(session, sessionCookieName)
	if err != nil {
		return err
	}
	ttl := session.ExpiresAt.Sub(m.now())
	if ttl <= 0 {
		return auth.ErrUnauthenticated
	}
	http.SetCookie(response, &http.Cookie{Name: sessionCookieName, Value: encoded, Path: "/", HttpOnly: true,
		Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()), Expires: session.ExpiresAt})
	return nil
}

func (m *SessionManager) Read(request *http.Request) (Session, error) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, auth.ErrUnauthenticated
	}
	var session Session
	if err := m.codec.decode(cookie.Value, sessionCookieName, &session); err != nil {
		return Session{}, err
	}
	if session.Version != 2 || session.ID == "" || session.TenantID == "" || session.Identity.TenantID != session.TenantID ||
		!session.Identity.Role.Valid() || session.CSRF == "" || !m.now().Before(session.ExpiresAt) {
		return Session{}, auth.ErrUnauthenticated
	}
	if m.Store != nil {
		identity, err := m.Store.ResolveBrowserSession(request.Context(), session.ID, session.TenantID)
		if err != nil {
			return Session{}, auth.ErrUnauthenticated
		}
		session.Identity = identity
	}
	return session, nil
}

func (m *SessionManager) Clear(response http.ResponseWriter) {
	for _, name := range []string{sessionCookieName, loginCookieName} {
		http.SetCookie(response, &http.Cookie{Name: name, Path: "/", HttpOnly: true,
			Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
}

func (m *SessionManager) setLogin(response http.ResponseWriter, state loginState) error {
	encoded, err := m.codec.encode(state, loginCookieName)
	if err != nil {
		return err
	}
	http.SetCookie(response, &http.Cookie{Name: loginCookieName, Value: encoded, Path: "/auth/callback", HttpOnly: true,
		Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	return nil
}

func (m *SessionManager) readLogin(request *http.Request) (loginState, error) {
	cookie, err := request.Cookie(loginCookieName)
	if err != nil {
		return loginState{}, auth.ErrUnauthenticated
	}
	var state loginState
	if err := m.codec.decode(cookie.Value, loginCookieName, &state); err != nil {
		return loginState{}, err
	}
	if state.Version != 1 || state.TenantID == "" || state.State == "" || state.Nonce == "" ||
		state.PKCEVerifier == "" || state.TokenURL == "" || !m.now().Before(state.ExpiresAt) {
		return loginState{}, auth.ErrUnauthenticated
	}
	return state, nil
}

type TenantContext struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type OIDCProviderRepository interface {
	ResolveTenant(context.Context, string) (store.Tenant, error)
	ListOIDCProviders(context.Context, string) ([]auth.OIDCProvider, error)
}

type OIDCLoginVerifier interface {
	AuthenticateOIDC(context.Context, string, string, string) (auth.Identity, error)
}

type OIDCFlow struct {
	repository OIDCProviderRepository
	verifier   OIDCLoginVerifier
	client     interface {
		Do(*http.Request) (*http.Response, error)
	}
	sessions  *SessionManager
	publicURL *url.URL
	allowHTTP bool
	now       func() time.Time
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

func NewOIDCFlow(repository OIDCProviderRepository, verifier OIDCLoginVerifier, client interface {
	Do(*http.Request) (*http.Response, error)
}, sessions *SessionManager, publicURL string, allowHTTP bool) (*OIDCFlow, error) {
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return nil, errors.New("controlplane: public URL must be an absolute HTTPS URL")
	}
	if repository == nil || verifier == nil || client == nil || sessions == nil {
		return nil, errors.New("controlplane: OIDC flow dependencies are required")
	}
	return &OIDCFlow{repository: repository, verifier: verifier, client: client, sessions: sessions,
		publicURL: parsed, allowHTTP: allowHTTP, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (f *OIDCFlow) Start(response http.ResponseWriter, request *http.Request, tenantKey string) error {
	tenant, err := f.repository.ResolveTenant(request.Context(), tenantKey)
	if err != nil {
		return err
	}
	providers, err := f.repository.ListOIDCProviders(request.Context(), tenant.ID)
	if err != nil {
		return err
	}
	wantedIssuer := strings.TrimSpace(request.URL.Query().Get("issuer"))
	var provider auth.OIDCProvider
	for _, candidate := range providers {
		if wantedIssuer == "" || candidate.Issuer == wantedIssuer {
			if provider.ID != "" && wantedIssuer == "" {
				return errors.New("controlplane: issuer query is required when multiple OIDC providers are enabled")
			}
			provider = candidate
		}
	}
	if provider.ID == "" {
		return store.ErrNotFound
	}
	discovery, err := f.discover(request.Context(), provider.Issuer)
	if err != nil {
		return err
	}
	state, err := randomToken(32)
	if err != nil {
		return err
	}
	nonce, err := randomToken(32)
	if err != nil {
		return err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return err
	}
	challenge := sha256.Sum256([]byte(verifier))
	login := loginState{Invitation: request.URL.Query().Get("invitation"), Version: 1, TenantID: tenant.ID, TenantKey: tenantKey, Issuer: provider.Issuer,
		ClientID: provider.ClientID, State: state, Nonce: nonce, PKCEVerifier: verifier,
		TokenURL: discovery.TokenEndpoint, ExpiresAt: f.now().Add(10 * time.Minute)}
	if err := f.sessions.setLogin(response, login); err != nil {
		return err
	}
	authorizationURL, _ := url.Parse(discovery.AuthorizationEndpoint)
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", provider.ClientID)
	query.Set("redirect_uri", f.callbackURL())
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	authorizationURL.RawQuery = query.Encode()
	http.Redirect(response, request, authorizationURL.String(), http.StatusFound)
	return nil
}

func (f *OIDCFlow) Callback(response http.ResponseWriter, request *http.Request) error {
	state, err := f.sessions.readLogin(request)
	if err != nil {
		return err
	}
	if request.URL.Query().Get("error") != "" {
		return fmt.Errorf("controlplane: OIDC provider returned %s", request.URL.Query().Get("error"))
	}
	actualState := request.URL.Query().Get("state")
	if len(actualState) != len(state.State) || subtle.ConstantTimeCompare([]byte(actualState), []byte(state.State)) != 1 {
		return auth.ErrUnauthenticated
	}
	code := request.URL.Query().Get("code")
	if code == "" || len(code) > 8192 {
		return auth.ErrUnauthenticated
	}
	idToken, err := f.exchange(request.Context(), state, code)
	if err != nil {
		return err
	}
	identity, err := f.verifier.AuthenticateOIDC(auth.WithInvitation(request.Context(), state.Invitation), state.TenantID, idToken, state.Nonce)
	if err != nil {
		return auth.ErrUnauthenticated
	}
	tenant, err := f.repository.ResolveTenant(request.Context(), state.TenantID)
	if err != nil {
		return err
	}
	session, err := f.sessions.NewSession(TenantContext{ID: tenant.ID, Key: state.TenantKey, Slug: tenant.Slug, Name: tenant.Name}, identity)
	if err != nil {
		return err
	}
	if err := f.sessions.Set(response, session); err != nil {
		return err
	}
	http.SetCookie(response, &http.Cookie{Name: loginCookieName, Path: "/auth/callback", HttpOnly: true,
		Secure: f.sessions.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(response, request, "/ui/?tenant="+url.QueryEscape(tenant.Slug), http.StatusFound)
	return nil
}

func (f *OIDCFlow) discover(ctx context.Context, issuer string) (oidcDiscovery, error) {
	metadataURL := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	var discovery oidcDiscovery
	if err := f.fetchJSON(ctx, http.MethodGet, metadataURL, "", &discovery); err != nil {
		return oidcDiscovery{}, err
	}
	if discovery.Issuer != issuer || !f.validEndpoint(discovery.AuthorizationEndpoint) || !f.validEndpoint(discovery.TokenEndpoint) {
		return oidcDiscovery{}, errors.New("controlplane: invalid OIDC discovery document")
	}
	return discovery, nil
}

func (f *OIDCFlow) exchange(ctx context.Context, state loginState, code string) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {state.ClientID},
		"redirect_uri": {f.callbackURL()}, "code_verifier": {state.PKCEVerifier}}
	var response struct {
		IDToken string `json:"id_token"`
	}
	if err := f.fetchJSON(ctx, http.MethodPost, state.TokenURL, form.Encode(), &response); err != nil {
		return "", err
	}
	if response.IDToken == "" || len(response.IDToken) > 32<<10 {
		return "", errors.New("controlplane: OIDC token response omitted id_token")
	}
	return response.IDToken, nil
}

func (f *OIDCFlow) fetchJSON(ctx context.Context, method, target, form string, result any) error {
	var body io.Reader
	if form != "" {
		body = strings.NewReader(form)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if form != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := f.client.Do(request)
	if err != nil {
		return fmt.Errorf("controlplane: OIDC request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("controlplane: OIDC endpoint returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, (1<<20)+1))
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("controlplane: decode OIDC response: %w", err)
	}
	return nil
}

func (f *OIDCFlow) validEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && parsed.User == nil && parsed.Fragment == "" &&
		(parsed.Scheme == "https" || (f.allowHTTP && parsed.Scheme == "http"))
}

func (f *OIDCFlow) callbackURL() string {
	result := *f.publicURL
	result.Path, result.RawPath, result.RawQuery, result.Fragment = "/auth/callback", "", "", ""
	return result.String()
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
