package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maximumTokenBytes = 32 << 10
	maximumJWKSBytes  = 1 << 20
)

type OIDCProvider struct {
	ID             string
	TenantID       string
	Issuer         string
	ClientID       string
	JWKSURI        string
	AllowedDomains []string
	Enabled        bool
}

type Claims struct {
	Issuer          string
	Subject         string
	Audience        []string
	AuthorizedParty string
	ExpiresAt       time.Time
	IssuedAt        time.Time
	NotBefore       time.Time
	Email           string
	EmailVerified   bool
	DisplayName     string
}

type OIDCRepository interface {
	OIDCProvider(context.Context, string, string) (OIDCProvider, error)
	ResolveOIDCIdentity(context.Context, string, Claims) (Identity, error)
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type OIDCConfig struct {
	Clock             func() time.Time
	ClockSkew         time.Duration
	CacheTTL          time.Duration
	StaleTTL          time.Duration
	AllowHTTPForTests bool
}

type OIDCAuthenticator struct {
	repository OIDCRepository
	client     HTTPClient
	config     OIDCConfig
	mutex      sync.RWMutex
	cache      map[string]cachedKeySet
}

type cachedKeySet struct {
	keys       []verificationKey
	expiresAt  time.Time
	staleUntil time.Time
	jwksURI    string
	fetchedAt  time.Time
}

type verificationKey struct {
	kid string
	alg string
	key crypto.PublicKey
}

func NewOIDCAuthenticator(repository OIDCRepository, client HTTPClient, config OIDCConfig) (*OIDCAuthenticator, error) {
	if repository == nil || client == nil {
		return nil, errors.New("auth: OIDC repository and HTTP client are required")
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	if config.ClockSkew == 0 {
		config.ClockSkew = time.Minute
	}
	if config.CacheTTL == 0 {
		config.CacheTTL = 15 * time.Minute
	}
	if config.StaleTTL == 0 {
		config.StaleTTL = time.Hour
	}
	if config.ClockSkew < 0 || config.CacheTTL <= 0 || config.StaleTTL < 0 {
		return nil, errors.New("auth: invalid OIDC timing configuration")
	}
	return &OIDCAuthenticator{repository: repository, client: client, config: config, cache: make(map[string]cachedKeySet)}, nil
}

func (authenticator *OIDCAuthenticator) Authenticate(ctx context.Context, tenantID, token string) (Identity, error) {
	return authenticator.authenticate(ctx, tenantID, token, "")
}

// AuthenticateWithNonce verifies a browser authorization-code ID token and
// binds it to the nonce generated before the redirect.
func (authenticator *OIDCAuthenticator) AuthenticateWithNonce(ctx context.Context, tenantID, token, expectedNonce string) (Identity, error) {
	if strings.TrimSpace(expectedNonce) == "" {
		return Identity{}, ErrUnauthenticated
	}
	return authenticator.authenticate(ctx, tenantID, token, expectedNonce)
}

func (authenticator *OIDCAuthenticator) authenticate(ctx context.Context, tenantID, token, expectedNonce string) (Identity, error) {
	header, rawClaims, signingInput, signature, err := parseJWT(token)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	provider, err := authenticator.repository.OIDCProvider(ctx, tenantID, rawClaims.Issuer)
	if err != nil || !provider.Enabled || provider.TenantID != tenantID || provider.Issuer != rawClaims.Issuer {
		return Identity{}, ErrUnauthenticated
	}
	if err := authenticator.verifyProvider(provider); err != nil {
		return Identity{}, ErrUnauthenticated
	}
	keys, err := authenticator.keys(ctx, provider, false)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	if !verifyWithKeys(header, signingInput, signature, keys) {
		keys, err = authenticator.keys(ctx, provider, true)
		if err != nil || !verifyWithKeys(header, signingInput, signature, keys) {
			return Identity{}, ErrUnauthenticated
		}
	}
	claims, err := validateClaims(rawClaims, provider, authenticator.config.Clock(), authenticator.config.ClockSkew)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	if expectedNonce != "" && (len(rawClaims.Nonce) != len(expectedNonce) || subtle.ConstantTimeCompare([]byte(rawClaims.Nonce), []byte(expectedNonce)) != 1) {
		return Identity{}, ErrUnauthenticated
	}
	identity, err := authenticator.repository.ResolveOIDCIdentity(ctx, tenantID, claims)
	if err != nil || identity.TenantID != tenantID || !identity.Role.Valid() {
		return Identity{}, ErrUnauthenticated
	}
	return identity, nil
}

func (authenticator *OIDCAuthenticator) verifyProvider(provider OIDCProvider) error {
	issuer, err := url.Parse(provider.Issuer)
	if err != nil || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("auth: invalid issuer")
	}
	if issuer.Scheme != "https" && !(authenticator.config.AllowHTTPForTests && issuer.Scheme == "http") {
		return errors.New("auth: issuer must use HTTPS")
	}
	if strings.TrimSpace(provider.ClientID) == "" {
		return errors.New("auth: client ID is required")
	}
	if provider.JWKSURI != "" {
		jwksURI, err := url.Parse(provider.JWKSURI)
		if err != nil || jwksURI.Host == "" || jwksURI.User != nil || jwksURI.Fragment != "" ||
			(jwksURI.Scheme != "https" && !(authenticator.config.AllowHTTPForTests && jwksURI.Scheme == "http")) {
			return errors.New("auth: invalid JWKS URI")
		}
	}
	return nil
}

type jwtHeader struct {
	Algorithm string   `json:"alg"`
	KeyID     string   `json:"kid"`
	Type      string   `json:"typ"`
	Critical  []string `json:"crit"`
}

type rawJWTClaims struct {
	Issuer          string      `json:"iss"`
	Subject         string      `json:"sub"`
	Audience        audience    `json:"aud"`
	AuthorizedParty string      `json:"azp"`
	ExpiresAt       json.Number `json:"exp"`
	IssuedAt        json.Number `json:"iat"`
	NotBefore       json.Number `json:"nbf"`
	Email           string      `json:"email"`
	EmailVerified   *bool       `json:"email_verified"`
	Name            string      `json:"name"`
	Nonce           string      `json:"nonce"`
}

type audience []string

func (values *audience) UnmarshalJSON(data []byte) error {
	var single string
	if json.Unmarshal(data, &single) == nil {
		*values = audience{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	*values = multiple
	return nil
}

func parseJWT(token string) (jwtHeader, rawJWTClaims, string, []byte, error) {
	if len(token) == 0 || len(token) > maximumTokenBytes {
		return jwtHeader{}, rawJWTClaims{}, "", nil, ErrUnauthenticated
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return jwtHeader{}, rawJWTClaims{}, "", nil, ErrUnauthenticated
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtHeader{}, rawJWTClaims{}, "", nil, err
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtHeader{}, rawJWTClaims{}, "", nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtHeader{}, rawJWTClaims{}, "", nil, err
	}
	var header jwtHeader
	if err := decodeStrictJSON(headerBytes, &header); err != nil || len(header.Critical) != 0 || !allowedAlgorithm(header.Algorithm) {
		return jwtHeader{}, rawJWTClaims{}, "", nil, ErrUnauthenticated
	}
	var claims rawJWTClaims
	if err := decodeStrictJSON(payloadBytes, &claims); err != nil || claims.Issuer == "" {
		return jwtHeader{}, rawJWTClaims{}, "", nil, ErrUnauthenticated
	}
	return header, claims, parts[0] + "." + parts[1], signature, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("auth: trailing JSON data")
	}
	return nil
}

func allowedAlgorithm(algorithm string) bool {
	switch algorithm {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA":
		return true
	default:
		return false
	}
}

func validateClaims(raw rawJWTClaims, provider OIDCProvider, now time.Time, skew time.Duration) (Claims, error) {
	if raw.Issuer != provider.Issuer || strings.TrimSpace(raw.Subject) == "" || len(raw.Subject) > 512 {
		return Claims{}, ErrUnauthenticated
	}
	if !contains([]string(raw.Audience), provider.ClientID) {
		return Claims{}, ErrUnauthenticated
	}
	if len(raw.Audience) > 1 && raw.AuthorizedParty != provider.ClientID {
		return Claims{}, ErrUnauthenticated
	}
	expiresAt, err := numericDate(raw.ExpiresAt, true)
	if err != nil || !now.Before(expiresAt.Add(skew)) {
		return Claims{}, ErrUnauthenticated
	}
	issuedAt, err := numericDate(raw.IssuedAt, false)
	if err != nil || (!issuedAt.IsZero() && issuedAt.After(now.Add(skew))) {
		return Claims{}, ErrUnauthenticated
	}
	notBefore, err := numericDate(raw.NotBefore, false)
	if err != nil || (!notBefore.IsZero() && now.Add(skew).Before(notBefore)) {
		return Claims{}, ErrUnauthenticated
	}
	emailVerified := raw.EmailVerified != nil && *raw.EmailVerified
	if len(provider.AllowedDomains) > 0 {
		if !emailVerified || !emailDomainAllowed(raw.Email, provider.AllowedDomains) {
			return Claims{}, ErrUnauthenticated
		}
	}
	return Claims{Issuer: raw.Issuer, Subject: raw.Subject, Audience: append([]string(nil), raw.Audience...),
		AuthorizedParty: raw.AuthorizedParty, ExpiresAt: expiresAt, IssuedAt: issuedAt,
		NotBefore: notBefore, Email: strings.TrimSpace(raw.Email), EmailVerified: emailVerified,
		DisplayName: strings.TrimSpace(raw.Name)}, nil
}

func numericDate(value json.Number, required bool) (time.Time, error) {
	if value == "" {
		if required {
			return time.Time{}, ErrUnauthenticated
		}
		return time.Time{}, nil
	}
	seconds, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func emailDomainAllowed(email string, allowed []string) bool {
	index := strings.LastIndex(strings.TrimSpace(email), "@")
	if index <= 0 || index == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[index+1:])
	for _, candidate := range allowed {
		if subtle.ConstantTimeCompare([]byte(domain), []byte(strings.ToLower(strings.TrimSpace(candidate)))) == 1 {
			return true
		}
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (authenticator *OIDCAuthenticator) keys(ctx context.Context, provider OIDCProvider, force bool) ([]verificationKey, error) {
	now := authenticator.config.Clock()
	authenticator.mutex.RLock()
	cached, found := authenticator.cache[provider.ID]
	authenticator.mutex.RUnlock()
	if !force && found && cached.jwksURI != "" && now.Before(cached.expiresAt) {
		return cached.keys, nil
	}
	// Unknown-kid/bad-signature traffic must not turn into an unbounded JWKS
	// fetch amplifier. A rotation is picked up within one minute at worst.
	if force && found && now.Before(cached.fetchedAt.Add(time.Minute)) {
		return cached.keys, nil
	}
	jwksURI := strings.TrimSpace(provider.JWKSURI)
	if jwksURI == "" {
		var err error
		jwksURI, err = authenticator.discoverJWKS(ctx, provider)
		if err != nil {
			return authenticator.staleKeys(provider.ID, now, err)
		}
	}
	keys, ttl, err := authenticator.fetchJWKS(ctx, jwksURI)
	if err != nil {
		return authenticator.staleKeys(provider.ID, now, err)
	}
	entry := cachedKeySet{keys: keys, expiresAt: now.Add(ttl), staleUntil: now.Add(ttl + authenticator.config.StaleTTL), jwksURI: jwksURI, fetchedAt: now}
	authenticator.mutex.Lock()
	authenticator.cache[provider.ID] = entry
	authenticator.mutex.Unlock()
	return keys, nil
}

func (authenticator *OIDCAuthenticator) staleKeys(providerID string, now time.Time, fetchError error) ([]verificationKey, error) {
	authenticator.mutex.RLock()
	cached, found := authenticator.cache[providerID]
	authenticator.mutex.RUnlock()
	if found && now.Before(cached.staleUntil) {
		return cached.keys, nil
	}
	return nil, fetchError
}

func (authenticator *OIDCAuthenticator) discoverJWKS(ctx context.Context, provider OIDCProvider) (string, error) {
	discoveryURL := strings.TrimSuffix(provider.Issuer, "/") + "/.well-known/openid-configuration"
	var document struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if _, err := authenticator.fetchJSON(ctx, discoveryURL, &document); err != nil {
		return "", err
	}
	if document.Issuer != provider.Issuer {
		return "", errors.New("auth: discovery issuer mismatch")
	}
	parsed, err := url.Parse(document.JWKSURI)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && !(authenticator.config.AllowHTTPForTests && parsed.Scheme == "http")) {
		return "", errors.New("auth: invalid JWKS URI")
	}
	return document.JWKSURI, nil
}

func (authenticator *OIDCAuthenticator) fetchJWKS(ctx context.Context, uri string) ([]verificationKey, time.Duration, error) {
	var document struct {
		Keys []struct {
			KeyType   string   `json:"kty"`
			KeyID     string   `json:"kid"`
			Use       string   `json:"use"`
			KeyOps    []string `json:"key_ops"`
			Algorithm string   `json:"alg"`
			N         string   `json:"n"`
			E         string   `json:"e"`
			Curve     string   `json:"crv"`
			X         string   `json:"x"`
			Y         string   `json:"y"`
		} `json:"keys"`
	}
	ttl, err := authenticator.fetchJSON(ctx, uri, &document)
	if err != nil {
		return nil, 0, err
	}
	if len(document.Keys) == 0 || len(document.Keys) > 128 {
		return nil, 0, errors.New("auth: invalid JWKS key count")
	}
	keys := make([]verificationKey, 0, len(document.Keys))
	for _, item := range document.Keys {
		if item.Use != "" && item.Use != "sig" {
			continue
		}
		if len(item.KeyOps) > 0 && !contains(item.KeyOps, "verify") {
			continue
		}
		key, parseErr := parseJWK(item.KeyType, item.Curve, item.N, item.E, item.X, item.Y)
		if parseErr != nil {
			continue
		}
		keys = append(keys, verificationKey{kid: item.KeyID, alg: item.Algorithm, key: key})
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("auth: JWKS contains no usable signing key")
	}
	return keys, ttl, nil
}

func (authenticator *OIDCAuthenticator) fetchJSON(ctx context.Context, uri string, target any) (time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := authenticator.client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("auth: fetch OIDC metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("auth: OIDC metadata HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumJWKSBytes+1))
	if err != nil {
		return 0, fmt.Errorf("auth: read OIDC metadata: %w", err)
	}
	if len(body) > maximumJWKSBytes {
		return 0, errors.New("auth: OIDC metadata exceeds 1 MiB")
	}
	if err := decodeStrictJSON(body, target); err != nil {
		return 0, fmt.Errorf("auth: decode OIDC metadata: %w", err)
	}
	ttl := cacheTTL(response.Header.Get("Cache-Control"), authenticator.config.CacheTTL)
	return ttl, nil
}

func cacheTTL(cacheControl string, fallback time.Duration) time.Duration {
	for _, directive := range strings.Split(cacheControl, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(directive), "=")
		if ok && strings.EqualFold(name, "max-age") {
			seconds, err := strconv.Atoi(strings.Trim(value, `"`))
			if err == nil && seconds >= 60 {
				ttl := time.Duration(seconds) * time.Second
				if ttl > 24*time.Hour {
					return 24 * time.Hour
				}
				return ttl
			}
		}
	}
	return fallback
}

func parseJWK(keyType, curve, encodedN, encodedE, encodedX, encodedY string) (crypto.PublicKey, error) {
	decode := func(value string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(value) }
	switch keyType {
	case "RSA":
		nBytes, err := decode(encodedN)
		if err != nil || len(nBytes) < 256 {
			return nil, errors.New("auth: invalid RSA modulus")
		}
		eBytes, err := decode(encodedE)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, errors.New("auth: invalid RSA exponent")
		}
		exponent := 0
		for _, value := range eBytes {
			exponent = exponent<<8 | int(value)
		}
		if exponent < 3 || exponent%2 == 0 {
			return nil, errors.New("auth: invalid RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}, nil
	case "EC":
		var selected elliptic.Curve
		switch curve {
		case "P-256":
			selected = elliptic.P256()
		case "P-384":
			selected = elliptic.P384()
		case "P-521":
			selected = elliptic.P521()
		default:
			return nil, errors.New("auth: unsupported EC curve")
		}
		xBytes, errX := decode(encodedX)
		yBytes, errY := decode(encodedY)
		if errX != nil || errY != nil {
			return nil, errors.New("auth: invalid EC point")
		}
		x, y := new(big.Int).SetBytes(xBytes), new(big.Int).SetBytes(yBytes)
		if !selected.IsOnCurve(x, y) {
			return nil, errors.New("auth: EC point is not on curve")
		}
		return &ecdsa.PublicKey{Curve: selected, X: x, Y: y}, nil
	case "OKP":
		if curve != "Ed25519" {
			return nil, errors.New("auth: unsupported OKP curve")
		}
		key, err := decode(encodedX)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("auth: invalid Ed25519 key")
		}
		return ed25519.PublicKey(key), nil
	default:
		return nil, errors.New("auth: unsupported key type")
	}
}

func verifyWithKeys(header jwtHeader, signingInput string, signature []byte, keys []verificationKey) bool {
	for _, candidate := range keys {
		if header.KeyID != "" && candidate.kid != header.KeyID {
			continue
		}
		if candidate.alg != "" && candidate.alg != header.Algorithm {
			continue
		}
		if verifySignature(header.Algorithm, candidate.key, []byte(signingInput), signature) {
			return true
		}
	}
	return false
}

func verifySignature(algorithm string, key crypto.PublicKey, message, signature []byte) bool {
	hash, digest, ok := signingDigest(algorithm, message)
	if !ok {
		return false
	}
	switch publicKey := key.(type) {
	case *rsa.PublicKey:
		switch algorithm[:2] {
		case "RS":
			return rsa.VerifyPKCS1v15(publicKey, hash, digest, signature) == nil
		case "PS":
			return rsa.VerifyPSS(publicKey, hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		}
	case *ecdsa.PublicKey:
		if !strings.HasPrefix(algorithm, "ES") {
			return false
		}
		if (algorithm == "ES256" && publicKey.Curve != elliptic.P256()) ||
			(algorithm == "ES384" && publicKey.Curve != elliptic.P384()) ||
			(algorithm == "ES512" && publicKey.Curve != elliptic.P521()) {
			return false
		}
		width := (publicKey.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*width {
			return false
		}
		return ecdsa.Verify(publicKey, digest, new(big.Int).SetBytes(signature[:width]), new(big.Int).SetBytes(signature[width:]))
	case ed25519.PublicKey:
		return algorithm == "EdDSA" && ed25519.Verify(publicKey, message, signature)
	}
	return false
}

func signingDigest(algorithm string, message []byte) (crypto.Hash, []byte, bool) {
	switch algorithm {
	case "RS256", "PS256", "ES256":
		digest := sha256.Sum256(message)
		return crypto.SHA256, digest[:], true
	case "RS384", "PS384", "ES384":
		digest := sha512.Sum384(message)
		return crypto.SHA384, digest[:], true
	case "RS512", "PS512", "ES512":
		digest := sha512.Sum512(message)
		return crypto.SHA512, digest[:], true
	case "EdDSA":
		return 0, nil, true
	default:
		return 0, nil, false
	}
}
