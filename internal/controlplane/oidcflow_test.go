package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/auth"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type oidcFlowRepositoryStub struct {
	tenant   store.Tenant
	provider auth.OIDCProvider
}

func (r *oidcFlowRepositoryStub) ResolveTenant(_ context.Context, key string) (store.Tenant, error) {
	if key == r.tenant.ID || key == r.tenant.Slug {
		return r.tenant, nil
	}
	return store.Tenant{}, store.ErrNotFound
}

func (r *oidcFlowRepositoryStub) ListOIDCProviders(_ context.Context, tenantID string) ([]auth.OIDCProvider, error) {
	if tenantID != r.tenant.ID {
		return nil, store.ErrNotFound
	}
	return []auth.OIDCProvider{r.provider}, nil
}

type oidcFlowVerifierStub struct {
	tenantID string
	token    string
	nonce    string
}

func (v *oidcFlowVerifierStub) AuthenticateOIDC(_ context.Context, tenantID, token, nonce string) (auth.Identity, error) {
	v.tenantID, v.token, v.nonce = tenantID, token, nonce
	return auth.Identity{TenantID: tenantID, ActorType: "user", ActorID: "oidc-user", Role: auth.RoleAdmin}, nil
}

func TestOIDCFlowAuthorizationCodePKCECallback(t *testing.T) {
	var issuer string
	var tokenForm url.Values
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(response).Encode(map[string]string{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
			})
		case "/token":
			if request.Method != http.MethodPost {
				t.Fatalf("token method=%s", request.Method)
			}
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			tokenForm = request.PostForm
			_ = json.NewEncoder(response).Encode(map[string]string{"id_token": "signed-id-token"})
		default:
			http.NotFound(response, request)
		}
	}))
	defer provider.Close()
	issuer = provider.URL

	tenant := store.Tenant{ID: testTenantID, Slug: "acme", Name: "Acme"}
	repository := &oidcFlowRepositoryStub{tenant: tenant, provider: auth.OIDCProvider{
		ID: "provider-1", TenantID: tenant.ID, Issuer: issuer, ClientID: "statushub-client", Enabled: true,
	}}
	verifier := &oidcFlowVerifierStub{}
	sessions, err := NewSessionManager(make([]byte, 32), false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)
	sessions.now = func() time.Time { return now }
	flow, err := NewOIDCFlow(repository, verifier, provider.Client(), sessions, "http://console.example.test/base", true)
	if err != nil {
		t.Fatal(err)
	}
	flow.now = func() time.Time { return now }

	startRequest := httptest.NewRequest(http.MethodGet, "http://console.example.test/auth/acme/login", nil)
	startResponse := httptest.NewRecorder()
	if err := flow.Start(startResponse, startRequest, "acme"); err != nil {
		t.Fatal(err)
	}
	if startResponse.Code != http.StatusFound {
		t.Fatalf("start status=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	authorizationURL, err := url.Parse(startResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorizationURL.Query()
	for key, expected := range map[string]string{
		"response_type": "code", "client_id": "statushub-client", "redirect_uri": "http://console.example.test/auth/callback",
		"scope": "openid email profile", "code_challenge_method": "S256",
	} {
		if query.Get(key) != expected {
			t.Fatalf("authorization %s=%q want=%q", key, query.Get(key), expected)
		}
	}
	if query.Get("state") == "" || query.Get("nonce") == "" || query.Get("code_challenge") == "" {
		t.Fatal("authorization request omitted state, nonce, or PKCE challenge")
	}
	loginCookie := startResponse.Result().Cookies()[0]
	callbackRequest := httptest.NewRequest(http.MethodGet,
		"http://console.example.test/auth/callback?state="+url.QueryEscape(query.Get("state"))+"&code=authorization-code", nil)
	callbackRequest.AddCookie(loginCookie)
	callbackResponse := httptest.NewRecorder()
	if err := flow.Callback(callbackResponse, callbackRequest); err != nil {
		t.Fatal(err)
	}
	if callbackResponse.Code != http.StatusFound || callbackResponse.Header().Get("Location") != "/ui/?tenant=acme" {
		t.Fatalf("callback status=%d location=%q", callbackResponse.Code, callbackResponse.Header().Get("Location"))
	}
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("code") != "authorization-code" ||
		tokenForm.Get("client_id") != "statushub-client" || tokenForm.Get("redirect_uri") != "http://console.example.test/auth/callback" {
		t.Fatalf("token form=%v", tokenForm)
	}
	challenge := sha256.Sum256([]byte(tokenForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(challenge[:]) != query.Get("code_challenge") {
		t.Fatal("token exchange did not use the authorization request PKCE verifier")
	}
	if verifier.tenantID != tenant.ID || verifier.token != "signed-id-token" || verifier.nonce != query.Get("nonce") {
		t.Fatalf("verifier tenant=%q token=%q nonce_match=%v", verifier.tenantID, verifier.token, verifier.nonce == query.Get("nonce"))
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || strings.Contains(sessionCookie.Value, tenant.ID) {
		t.Fatal("encrypted session cookie was not set")
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "http://console.example.test/auth/session", nil)
	sessionRequest.AddCookie(sessionCookie)
	session, err := sessions.Read(sessionRequest)
	if err != nil || session.TenantID != tenant.ID || session.Identity.ActorID != "oidc-user" || session.CSRF == "" {
		t.Fatalf("session=%#v err=%v", session, err)
	}
}

func TestOIDCFlowRejectsStateMismatchBeforeTokenExchange(t *testing.T) {
	sessions, err := NewSessionManager(make([]byte, 32), false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state := loginState{Version: 1, TenantID: testTenantID, TenantKey: "acme", Issuer: "https://issuer.example",
		ClientID: "client", State: "expected", Nonce: "nonce", PKCEVerifier: "verifier",
		TokenURL: "https://issuer.example/token", ExpiresAt: time.Now().Add(time.Minute)}
	recorder := httptest.NewRecorder()
	if err := sessions.setLogin(recorder, state); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://console.example/auth/callback?state=wrong&code=code", nil)
	request.AddCookie(recorder.Result().Cookies()[0])
	flow := &OIDCFlow{sessions: sessions}
	if err := flow.Callback(httptest.NewRecorder(), request); err == nil {
		t.Fatal("state mismatch was accepted")
	}
}
