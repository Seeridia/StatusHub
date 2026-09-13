// Package statuspage implements the read-only Atlassian Statuspage public v2
// API. It does not use the authenticated Statuspage management API.
package statuspage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	adaptercontract "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/transport"
)

const (
	SummaryPath             = "/api/v2/summary.json"
	IncidentsPath           = "/api/v2/incidents.json"
	UnresolvedIncidentsPath = "/api/v2/incidents/unresolved.json"

	defaultCapabilityTTL = 24 * time.Hour
	defaultMaximumBody   = int64(2 << 20)
)

var _ adaptercontract.Adapter = (*Adapter)(nil)

// Adapter depends on the safe transport's narrow interface. No default
// http.Client is constructed here, so DNS, redirect, timeout, and body-limit
// policy cannot accidentally be bypassed by an adapter call site.
type Adapter struct {
	client        transport.Client
	now           func() time.Time
	capabilityTTL time.Duration
}

type Option func(*Adapter) error

// WithClock supplies the collector clock, mainly for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(adapter *Adapter) error {
		if now == nil {
			return errors.New("statuspage adapter: clock is nil")
		}
		adapter.now = now
		return nil
	}
}

// WithCapabilityTTL controls how long a successful probe may be cached.
func WithCapabilityTTL(ttl time.Duration) Option {
	return func(adapter *Adapter) error {
		if ttl <= 0 {
			return errors.New("statuspage adapter: capability TTL must be positive")
		}
		adapter.capabilityTTL = ttl
		return nil
	}
}

func New(client transport.Client, options ...Option) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("statuspage adapter: transport client is required")
	}
	result := &Adapter{
		client:        client,
		now:           time.Now,
		capabilityTTL: defaultCapabilityTTL,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("statuspage adapter: option is nil")
		}
		if err := option(result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Probe performs two bounded reads. Each endpoint is validated independently;
// one unavailable endpoint does not erase a successfully discovered one.
func (a *Adapter) Probe(ctx context.Context, target domain.Target) (domain.Capabilities, error) {
	if a == nil || a.client == nil {
		return domain.Capabilities{}, errors.New("statuspage adapter: transport client is required")
	}
	if ctx == nil {
		return domain.Capabilities{}, errors.New("statuspage adapter: context is nil")
	}
	if target.URL == nil {
		return domain.Capabilities{}, errors.New("statuspage adapter: target URL is required")
	}

	definitions := []probeDefinition{
		{
			resource: domain.ResourceSummary,
			path:     SummaryPath,
			decode: func(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
				return DecodeSummary(body, source, observedAt)
			},
		},
		{
			resource: domain.ResourceUnresolvedIncidents,
			path:     UnresolvedIncidentsPath,
			decode: func(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
				return DecodeUnresolvedIncidents(body, source, observedAt)
			},
		},
	}

	endpoints := make(map[domain.ResourceKind]domain.EndpointCapability, len(definitions))
	schemaHashes := make(map[domain.ResourceKind]string, len(definitions))
	probeErrors := make([]error, 0, len(definitions))
	allFailuresAreNegativeEvidence := true
	supportsETag := false
	supportsModified := false
	summaryFound := false

	for _, definition := range definitions {
		if err := ctx.Err(); err != nil {
			return domain.Capabilities{}, err
		}
		result, err := a.probeEndpoint(ctx, target, definition)
		if err != nil {
			probeErrors = append(probeErrors, fmt.Errorf("%s: %w", definition.path, err))
			allFailuresAreNegativeEvidence = allFailuresAreNegativeEvidence && isNegativeProbeError(err)
			continue
		}
		endpoints[definition.resource] = result.capability
		schemaHashes[definition.resource] = result.schemaHash
		supportsETag = supportsETag || result.capability.Cache.SupportsETag
		supportsModified = supportsModified || result.capability.Cache.SupportsLastModified
		summaryFound = summaryFound || definition.resource == domain.ResourceSummary
	}

	if len(endpoints) == 0 {
		joined := errors.Join(probeErrors...)
		if allFailuresAreNegativeEvidence {
			return domain.Capabilities{}, fmt.Errorf("%w: %w", ErrNotStatuspage, joined)
		}
		return domain.Capabilities{}, joined
	}

	confidence := 0.88
	if summaryFound {
		confidence = 0.98
	}
	if len(endpoints) == len(definitions) {
		confidence = 1
	}
	now := a.now().UTC()
	return domain.Capabilities{
		Engine:               Engine,
		Version:              Version,
		Confidence:           confidence,
		Endpoints:            endpoints,
		Webhook:              domain.WebhookCapability{},
		SupportsETag:         supportsETag,
		SupportsModified:     supportsModified,
		SnapshotCompleteness: domain.CompletenessComplete,
		SchemaHash:           combinedSchemaHash(schemaHashes),
		ExpiresAt:            now.Add(a.capabilityTTL),
	}, nil
}

func isNegativeProbeError(err error) bool {
	if errors.Is(err, ErrInvalidPayload) {
		return true
	}
	var statusError *HTTPStatusError
	if !errors.As(err, &statusError) {
		return false
	}
	return statusError.StatusCode == http.StatusNotFound || statusError.StatusCode == http.StatusGone
}

type probeDefinition struct {
	resource domain.ResourceKind
	path     string
	decode   func([]byte, domain.Source, time.Time) (domain.Snapshot, error)
}

type probeResult struct {
	capability domain.EndpointCapability
	schemaHash string
}

func (a *Adapter) probeEndpoint(ctx context.Context, target domain.Target, definition probeDefinition) (probeResult, error) {
	endpoint, err := endpointURL(target.URL, definition.path)
	if err != nil {
		return probeResult{}, err
	}
	response, err := a.client.Get(ctx, endpoint, transport.Conditional{})
	if err != nil {
		return probeResult{}, err
	}
	if response == nil {
		return probeResult{}, errors.New("transport returned a nil response")
	}
	if response.StatusCode != http.StatusOK {
		return probeResult{}, &HTTPStatusError{URL: endpoint, StatusCode: response.StatusCode}
	}

	observedAt := responseObservedAt(response, a.now)
	source := sourceFromTarget(target)
	snapshot, err := definition.decode(response.Body, source, observedAt)
	if err != nil {
		return probeResult{}, err
	}
	fingerprint, err := schemaHash(response.Body)
	if err != nil {
		return probeResult{}, err
	}

	return probeResult{
		capability: domain.EndpointCapability{
			Resource:          definition.resource,
			Path:              definition.path,
			Completeness:      snapshot.Completeness,
			AuthoritativeFor:  append([]domain.ResourceKind(nil), snapshot.AuthoritativeFor...),
			Cache:             cacheCapability(response),
			Pagination:        domain.PaginationCapability{Kind: domain.PaginationNone},
			Authentication:    "none",
			MaximumBodyBytes:  defaultMaximumBody,
			ExpectedMediaType: "application/json",
		},
		schemaHash: fingerprint,
	}, nil
}

// Fetch retrieves and decodes one endpoint discovered by Probe.
func (a *Adapter) Fetch(ctx context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	if a == nil || a.client == nil {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("statuspage adapter: transport client is required")
	}
	if ctx == nil {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("statuspage adapter: context is nil")
	}
	if request.Target.URL == nil {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("statuspage adapter: target URL is required")
	}

	path, err := fetchPath(request)
	if err != nil {
		return domain.Snapshot{}, domain.FetchMeta{}, err
	}
	endpoint, err := endpointURL(request.Target.URL, path)
	if err != nil {
		return domain.Snapshot{}, domain.FetchMeta{}, err
	}

	conditional := transport.Conditional{ETag: request.ETag}
	if request.LastModified != nil {
		conditional.LastModified = request.LastModified.UTC().Format(http.TimeFormat)
	}
	response, err := a.client.Get(ctx, endpoint, conditional)
	if err != nil {
		return domain.Snapshot{}, domain.FetchMeta{}, err
	}
	if response == nil {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("statuspage adapter: transport returned a nil response")
	}

	observedAt := responseObservedAt(response, a.now)
	meta := fetchMeta(endpoint, response, observedAt)
	if response.NotModified || response.StatusCode == http.StatusNotModified {
		meta.NotModified = true
		return unchangedSnapshot(request, observedAt), meta, nil
	}
	meta.RawHash = rawHash(response.Body)
	if response.StatusCode != http.StatusOK {
		return domain.Snapshot{}, meta, &HTTPStatusError{
			URL:        endpoint,
			StatusCode: response.StatusCode,
			RetryAfter: parseRetryAfter(responseRetryAfter(response), observedAt),
		}
	}
	meta.SchemaHash, err = schemaHash(response.Body)
	if err != nil {
		return domain.Snapshot{}, meta, err
	}

	source := sourceForFetch(request)
	var snapshot domain.Snapshot
	switch request.ResourceKind {
	case domain.ResourceSummary:
		snapshot, err = DecodeSummary(response.Body, source, observedAt)
	case domain.ResourceIncidents:
		snapshot, err = DecodeIncidents(response.Body, source, observedAt)
	case domain.ResourceUnresolvedIncidents:
		snapshot, err = DecodeUnresolvedIncidents(response.Body, source, observedAt)
	default:
		return domain.Snapshot{}, meta, fmt.Errorf("%w: resource %q", ErrUnsupported, request.ResourceKind)
	}
	if err != nil {
		return domain.Snapshot{}, meta, err
	}

	return snapshot, meta, nil
}

func (a *Adapter) DecodeWebhook(context.Context, domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return nil, fmt.Errorf("%w: public subscriber webhooks are trigger-only and are not authoritative events", ErrUnsupported)
}

// HTTPStatusError keeps status classification machine-readable without
// retaining an upstream response body that may contain untrusted content.
type HTTPStatusError struct {
	URL        string
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("statuspage GET %s: HTTP %d", e.URL, e.StatusCode)
}

func fetchPath(request domain.FetchRequest) (string, error) {
	var defaultPath string
	switch request.ResourceKind {
	case domain.ResourceSummary:
		defaultPath = SummaryPath
	case domain.ResourceIncidents:
		defaultPath = IncidentsPath
	case domain.ResourceUnresolvedIncidents:
		defaultPath = UnresolvedIncidentsPath
	default:
		return "", fmt.Errorf("%w: resource %q", ErrUnsupported, request.ResourceKind)
	}

	path := strings.TrimSpace(request.Endpoint.Path)
	if path != "" {
		return path, nil
	}
	return defaultPath, nil
}

func endpointURL(base *url.URL, endpointPath string) (string, error) {
	if base == nil {
		return "", errors.New("statuspage adapter: target URL is required")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return "", fmt.Errorf("statuspage adapter: target scheme %q is not supported", base.Scheme)
	}
	if base.Host == "" || base.Opaque != "" || base.User != nil {
		return "", errors.New("statuspage adapter: target must be an absolute HTTP URL without user information")
	}

	reference, err := url.Parse(strings.TrimSpace(endpointPath))
	if err != nil {
		return "", fmt.Errorf("statuspage adapter: invalid endpoint path: %w", err)
	}
	if reference.IsAbs() || reference.Host != "" || reference.User != nil {
		return "", errors.New("statuspage adapter: endpoint must be a same-origin path")
	}
	if reference.Path == "" {
		return "", errors.New("statuspage adapter: endpoint path is required")
	}
	if !strings.HasPrefix(reference.Path, "/") {
		reference.Path = "/" + reference.Path
	}

	origin := *base
	origin.Path = "/"
	origin.RawPath = ""
	origin.RawQuery = ""
	origin.ForceQuery = false
	origin.Fragment = ""
	return origin.ResolveReference(reference).String(), nil
}

func sourceFromTarget(target domain.Target) domain.Source {
	return domain.Source{
		ID:           target.SourceID,
		Provider:     target.Provider,
		PageID:       target.PageID,
		Kind:         domain.SourceKindStatusPage,
		RequestedURL: target.URL,
	}
}

func sourceForFetch(request domain.FetchRequest) domain.Source {
	source := request.Source
	if source.ID == "" {
		source.ID = request.Target.SourceID
	}
	if source.Provider == "" {
		source.Provider = request.Target.Provider
	}
	if source.PageID == "" {
		source.PageID = request.Target.PageID
	}
	if source.RequestedURL == nil {
		source.RequestedURL = request.Target.URL
	}
	return source
}

func unchangedSnapshot(request domain.FetchRequest, observedAt time.Time) domain.Snapshot {
	return domain.Snapshot{
		Source:            sourceForFetch(request),
		ResourceKind:      request.ResourceKind,
		Completeness:      domain.CompletenessUnknown,
		ObservedAt:        observedAt,
		SchemaVersion:     Version,
		AdapterVersion:    AdapterVersion,
		NormalizerVersion: NormalizerVersion,
	}
}

func fetchMeta(endpoint string, response *transport.Response, observedAt time.Time) domain.FetchMeta {
	lastModified, _ := parseHTTPTime(response.LastModified)
	contentType := ""
	if response.Header != nil {
		contentType = response.Header.Get("Content-Type")
	}
	if response.FinalURL != "" {
		endpoint = response.FinalURL
	} else if response.URL != "" {
		endpoint = response.URL
	}
	return domain.FetchMeta{
		Endpoint:     endpoint,
		StatusCode:   response.StatusCode,
		ETag:         response.ETag,
		LastModified: lastModified,
		ObservedAt:   observedAt,
		NotModified:  response.NotModified,
		ContentType:  contentType,
		BodyBytes:    int64(len(response.Body)),
		RetryAfter:   parseRetryAfter(responseRetryAfter(response), observedAt),
	}
}

func responseObservedAt(response *transport.Response, fallback func() time.Time) time.Time {
	if !response.ObservedAt.IsZero() {
		return response.ObservedAt.UTC()
	}
	return fallback().UTC()
}

func responseRetryAfter(response *transport.Response) string {
	if response.RetryAfter != "" {
		return response.RetryAfter
	}
	return response.Header.Get("Retry-After")
}

func cacheCapability(response *transport.Response) domain.CacheCapability {
	return domain.CacheCapability{
		SupportsETag:         response.ETag != "",
		SupportsLastModified: response.LastModified != "",
		SupportsCacheControl: response.CacheControl != "",
		DefaultTTL:           maxAge(response.CacheControl),
	}
}

func maxAge(cacheControl string) time.Duration {
	for _, directive := range strings.Split(cacheControl, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if !found || !strings.EqualFold(name, "max-age") {
			continue
		}
		seconds, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64)
		if err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return 0
}

func parseHTTPTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func rawHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func combinedSchemaHash(values map[domain.ResourceKind]string) string {
	keys := make([]string, 0, len(values))
	for resource := range values {
		keys = append(keys, string(resource))
	}
	sort.Strings(keys)
	hash := sha256.New()
	_, _ = hash.Write([]byte("statuspage-probe-schema/v1\n"))
	for _, key := range keys {
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(values[domain.ResourceKind(key)]))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
