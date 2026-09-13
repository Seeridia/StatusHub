package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type oidcRepositoryStub struct {
	provider OIDCProvider
	identity Identity
}

func (repository *oidcRepositoryStub) OIDCProvider(_ context.Context, tenantID, issuer string) (OIDCProvider, error) {
	if tenantID != repository.provider.TenantID || issuer != repository.provider.Issuer {
		return OIDCProvider{}, ErrUnauthenticated
	}
	return repository.provider, nil
}

func (repository *oidcRepositoryStub) ResolveOIDCIdentity(_ context.Context, tenantID string, claims Claims) (Identity, error) {
	if tenantID != repository.identity.TenantID || claims.Subject != repository.identity.Subject {
		return Identity{}, ErrUnauthenticated
	}
	return repository.identity, nil
}

func TestOIDCAuthenticatorVerifiesJWKSClaimsDomainsAndCache(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	var jwksRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(response, `{"issuer":%q,"jwks_uri":%q}`, issuer, issuer+"/keys")
		case "/keys":
			jwksRequests.Add(1)
			response.Header().Set("Cache-Control", "public, max-age=3600")
			modulus := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
			_, _ = fmt.Fprintf(response, `{"keys":[{"kty":"RSA","kid":"key-1","use":"sig","alg":"RS256","n":%q,"e":"AQAB"}]}`, modulus)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	issuer = server.URL
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	repository := &oidcRepositoryStub{
		provider: OIDCProvider{ID: "provider-1", TenantID: "tenant-1", Issuer: issuer, ClientID: "statusmon",
			AllowedDomains: []string{"example.com"}, Enabled: true},
		identity: Identity{TenantID: "tenant-1", ActorType: "user", ActorID: "principal-1", Subject: "oidc-user", Role: RoleAdmin},
	}
	authenticator, err := NewOIDCAuthenticator(repository, server.Client(), OIDCConfig{
		Clock: func() time.Time { return now }, AllowHTTPForTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"iss": issuer, "sub": "oidc-user", "aud": "statusmon",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Add(-time.Minute).Unix(),
		"email": "operator@example.com", "email_verified": true, "name": "Operator", "nonce": "browser-nonce"}
	token := signRS256(t, privateKey, "key-1", payload)
	for attempt := 0; attempt < 2; attempt++ {
		identity, authenticateErr := authenticator.Authenticate(context.Background(), "tenant-1", token)
		if authenticateErr != nil || identity.ActorID != "principal-1" {
			t.Fatalf("attempt=%d identity=%#v err=%v", attempt, identity, authenticateErr)
		}
	}
	if jwksRequests.Load() != 1 {
		t.Fatalf("JWKS requests=%d want=1", jwksRequests.Load())
	}
	if _, err := authenticator.AuthenticateWithNonce(context.Background(), "tenant-1", token, "browser-nonce"); err != nil {
		t.Fatalf("nonce-bound authentication failed: %v", err)
	}
	if _, err := authenticator.AuthenticateWithNonce(context.Background(), "tenant-1", token, "wrong-nonce"); err == nil {
		t.Fatal("wrong OIDC nonce accepted")
	}
	if _, err := authenticator.Authenticate(context.Background(), "tenant-2", token); err == nil {
		t.Fatal("cross-tenant token accepted")
	}
	payload["email"] = "operator@attacker.test"
	if _, err := authenticator.Authenticate(context.Background(), "tenant-1", signRS256(t, privateKey, "key-1", payload)); err == nil {
		t.Fatal("disallowed email domain accepted")
	}
	payload["email"] = "operator@example.com"
	payload["exp"] = now.Add(-2 * time.Minute).Unix()
	if _, err := authenticator.Authenticate(context.Background(), "tenant-1", signRS256(t, privateKey, "key-1", payload)); err == nil {
		t.Fatal("expired token accepted")
	}
}

func signRS256(t *testing.T, privateKey *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	input := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}
