// Package ecosystem implements public, read-only adapters for common status
// page engines that are not Atlassian Statuspage. Capability probing is kept
// off the steady-state fetch path and every response is decoded by the engine
// selected during probing.
package ecosystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	adaptercontract "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/transport"
)

const defaultCapabilityTTL = 6 * time.Hour

type endpointDefinition struct {
	resource         domain.ResourceKind
	path             string
	completeness     domain.Completeness
	authoritativeFor []domain.ResourceKind
	decode           decoder
	required         bool
}

type engineDefinition struct {
	name       string
	version    string
	confidence float64
	endpoints  func(domain.Target) ([]endpointDefinition, error)
}

type Adapter struct {
	client        transport.Client
	now           func() time.Time
	capabilityTTL time.Duration
	recipes       map[string]compiledHTMLRecipe
}

var _ adaptercontract.Adapter = (*Adapter)(nil)

type Option func(*Adapter) error

func WithClock(now func() time.Time) Option {
	return func(adapter *Adapter) error {
		if now == nil {
			return errors.New("ecosystem adapter: clock is nil")
		}
		adapter.now = now
		return nil
	}
}

func WithHTMLRecipes(recipes ...HTMLRecipe) Option {
	return func(adapter *Adapter) error {
		for _, recipe := range recipes {
			compiled, err := compileHTMLRecipe(recipe)
			if err != nil {
				return err
			}
			if _, duplicate := adapter.recipes[compiled.host]; duplicate {
				return fmt.Errorf("ecosystem adapter: duplicate HTML recipe host %q", compiled.host)
			}
			adapter.recipes[compiled.host] = compiled
		}
		return nil
	}
}

func New(client transport.Client, options ...Option) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("ecosystem adapter: transport client is required")
	}
	result := &Adapter{client: client, now: time.Now, capabilityTTL: defaultCapabilityTTL, recipes: make(map[string]compiledHTMLRecipe)}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("ecosystem adapter: option is nil")
		}
		if err := option(result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (a *Adapter) Probe(ctx context.Context, target domain.Target) (domain.Capabilities, error) {
	if a == nil || a.client == nil || target.URL == nil {
		return domain.Capabilities{}, errors.New("ecosystem adapter: initialized adapter and target URL are required")
	}
	definitions := a.candidates(target)
	if len(definitions) == 0 {
		return domain.Capabilities{}, ErrNotRecognized
	}
	var failures []error
	for _, definition := range definitions {
		capabilities, err := a.probeEngine(ctx, target, definition)
		if err == nil {
			return capabilities, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", definition.name, err))
	}
	return domain.Capabilities{}, fmt.Errorf("%w: %w", ErrNotRecognized, errors.Join(failures...))
}

func (a *Adapter) probeEngine(ctx context.Context, target domain.Target, engine engineDefinition) (domain.Capabilities, error) {
	definitions, err := engine.endpoints(target)
	if err != nil {
		return domain.Capabilities{}, err
	}
	endpoints := make(map[domain.ResourceKind]domain.EndpointCapability, len(definitions))
	shapes := make([]string, 0, len(definitions))
	var optionalErrors []error
	for _, definition := range definitions {
		if _, alreadyFound := endpoints[definition.resource]; alreadyFound {
			continue
		}
		endpoint, err := resolveEndpoint(target.URL, definition.path)
		if err != nil {
			if definition.required {
				return domain.Capabilities{}, err
			}
			continue
		}
		response, err := a.client.Get(ctx, endpoint, transport.Conditional{})
		if err != nil || response == nil || response.StatusCode != http.StatusOK {
			if err == nil {
				status := 0
				if response != nil {
					status = response.StatusCode
				}
				err = fmt.Errorf("GET %s: HTTP %d", endpoint, status)
			}
			if definition.required {
				return domain.Capabilities{}, err
			}
			optionalErrors = append(optionalErrors, err)
			continue
		}
		observedAt := observedAt(response, a.now)
		source := sourceForEngine(domain.Source{ID: target.SourceID, Provider: engine.name, PageID: target.PageID,
			Kind: domain.SourceKindStatusPage, RequestedURL: target.URL, CanonicalURL: target.URL}, engine.name)
		if _, err := definition.decode(response.Body, source, observedAt, definition.resource); err != nil {
			if definition.required {
				return domain.Capabilities{}, err
			}
			optionalErrors = append(optionalErrors, err)
			continue
		}
		fingerprint := ""
		if engine.name == EngineHTMLRecipe {
			fingerprint = stableID("html-recipe-schema", definition.path)
		} else {
			fingerprint, err = schemaHash(response.Body)
			if err != nil {
				return domain.Capabilities{}, err
			}
		}
		shapes = append(shapes, string(definition.resource)+":"+fingerprint)
		endpoints[definition.resource] = domain.EndpointCapability{
			Resource: definition.resource, Path: definition.path, Completeness: definition.completeness,
			AuthoritativeFor: append([]domain.ResourceKind(nil), definition.authoritativeFor...),
			Cache:            cacheFromResponse(response), Pagination: domain.PaginationCapability{Kind: domain.PaginationNone},
			Authentication: "none", MaximumBodyBytes: defaultMaximumBody,
			ExpectedMediaType: expectedMediaType(engine.name),
		}
	}
	if len(endpoints) == 0 {
		return domain.Capabilities{}, errors.Join(optionalErrors...)
	}
	sort.Strings(shapes)
	combined := sha256.Sum256([]byte(strings.Join(shapes, "\n")))
	return domain.Capabilities{
		Engine: engine.name, Version: engine.version, Confidence: engine.confidence,
		Endpoints: endpoints, SupportsETag: anyCache(endpoints, func(cache domain.CacheCapability) bool { return cache.SupportsETag }),
		SupportsModified:     anyCache(endpoints, func(cache domain.CacheCapability) bool { return cache.SupportsLastModified }),
		SnapshotCompleteness: aggregateCompleteness(endpoints), SchemaHash: hex.EncodeToString(combined[:]),
		ExpiresAt: a.now().UTC().Add(a.capabilityTTL),
	}, nil
}

func (a *Adapter) Fetch(ctx context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	if a == nil || a.client == nil || request.Target.URL == nil {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("ecosystem adapter: initialized adapter and target URL are required")
	}
	engine, found := definitionByName(request.Source.Provider)
	if !found {
		engine, found = definitionByName(request.Target.Provider)
	}
	if !found && (request.Source.Provider == EngineHTMLRecipe || request.Target.Provider == EngineHTMLRecipe) {
		host := strings.ToLower(request.Target.URL.Hostname())
		if _, recipeFound := a.recipes[host]; recipeFound {
			engine, found = a.htmlDefinition(host), true
		}
	}
	if !found {
		return domain.Snapshot{}, domain.FetchMeta{}, fmt.Errorf("%w: engine %q", ErrUnsupported, request.Source.Provider)
	}
	definitions, err := engine.endpoints(request.Target)
	if err != nil {
		return domain.Snapshot{}, domain.FetchMeta{}, err
	}
	definition, found := endpointForResource(definitions, request.ResourceKind)
	if !found {
		return domain.Snapshot{}, domain.FetchMeta{}, fmt.Errorf("%w: engine %s resource %s", ErrUnsupported, engine.name, request.ResourceKind)
	}
	path := strings.TrimSpace(request.Endpoint.Path)
	if path == "" {
		path = definition.path
	}
	endpoint, err := resolveEndpoint(request.Target.URL, path)
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
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("ecosystem adapter: transport returned nil response")
	}
	observed := observedAt(response, a.now)
	meta := metaFromResponse(endpoint, response, observed)
	if response.NotModified || response.StatusCode == http.StatusNotModified {
		meta.NotModified = true
		return domain.Snapshot{Source: request.Source, ResourceKind: request.ResourceKind, Completeness: domain.CompletenessUnknown,
			ObservedAt: observed, SchemaVersion: engine.version, AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, meta, nil
	}
	meta.RawHash = stableID("raw", string(response.Body))
	if response.StatusCode != http.StatusOK {
		return domain.Snapshot{}, meta, &HTTPStatusError{Endpoint: endpoint, StatusCode: response.StatusCode, RetryAfter: meta.RetryAfter}
	}
	meta.SchemaHash, err = schemaHash(response.Body)
	if err != nil && engine.name != EngineHTMLRecipe {
		return domain.Snapshot{}, meta, err
	}
	source := request.Source
	if source.ID == "" {
		source.ID = request.Target.SourceID
	}
	if source.PageID == "" {
		source.PageID = request.Target.PageID
	}
	if source.RequestedURL == nil {
		source.RequestedURL = request.Target.URL
	}
	source = sourceForEngine(source, engine.name)
	snapshot, err := definition.decode(response.Body, source, observed, request.ResourceKind)
	if err != nil {
		return domain.Snapshot{}, meta, err
	}
	return snapshot, meta, nil
}

func (*Adapter) DecodeWebhook(context.Context, domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return nil, fmt.Errorf("%w: ecosystem public adapters are read-only", ErrUnsupported)
}

type HTTPStatusError struct {
	Endpoint   string
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("ecosystem GET %s: HTTP %d", e.Endpoint, e.StatusCode)
}

func (a *Adapter) candidates(target domain.Target) []engineDefinition {
	if definition, found := definitionByName(target.Provider); found {
		return []engineDefinition{definition}
	}
	host := strings.ToLower(target.URL.Hostname())
	for _, name := range hostEngineHints(host) {
		if definition, found := definitionByName(name); found {
			return []engineDefinition{definition}
		}
	}
	if _, found := a.recipes[host]; found {
		return []engineDefinition{a.htmlDefinition(host)}
	}
	// Incident.io widget URLs are intentionally not guessed: owners must supply
	// provider=incident-io because there is no universal public path.
	return []engineDefinition{
		definitionInstatus(), definitionBetterStack(), definitionCachet(),
		definitionGatus(), definitionCState(),
	}
}

func allDefinitions() []engineDefinition {
	return []engineDefinition{definitionIncidentIO(), definitionInstatus(), definitionBetterStack(), definitionStatusIO(), definitionCachet(), definitionGatus(), definitionCState()}
}

func definitionByName(name string) (engineDefinition, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	aliases := map[string]string{
		"incident.io": EngineIncidentIO, "incident-io": EngineIncidentIO,
		"betterstack": EngineBetterStack, "better-stack": EngineBetterStack,
		"status.io": EngineStatusIO, "status-io": EngineStatusIO,
		"cachet-v2": EngineCachet, "cachet-v3": EngineCachet,
	}
	if alias := aliases[name]; alias != "" {
		name = alias
	}
	for _, definition := range allDefinitions() {
		if definition.name == name {
			return definition, true
		}
	}
	return engineDefinition{}, false
}

func isEcosystemEngine(name string) bool {
	_, found := definitionByName(name)
	return found || name == EngineHTMLRecipe
}

func hostEngineHints(host string) []string {
	switch {
	case host == "api.status.io":
		return []string{EngineStatusIO}
	case strings.HasSuffix(host, ".instatus.com") || host == "instatus.com":
		return []string{EngineInstatus}
	case strings.HasSuffix(host, ".betteruptime.com") || host == "status.betterstack.com":
		return []string{EngineBetterStack}
	case strings.Contains(host, "cachet"):
		return []string{EngineCachet}
	case host == "status.twin.sh":
		return []string{EngineGatus}
	case strings.Contains(host, "cstate"):
		return []string{EngineCState}
	default:
		return nil
	}
}

func endpointForResource(definitions []endpointDefinition, resource domain.ResourceKind) (endpointDefinition, bool) {
	for _, definition := range definitions {
		if definition.resource == resource {
			return definition, true
		}
	}
	return endpointDefinition{}, false
}

func resolveEndpoint(base *url.URL, referenceValue string) (string, error) {
	if base == nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		return "", errors.New("ecosystem adapter: absolute HTTP(S) target without userinfo is required")
	}
	reference, err := url.Parse(strings.TrimSpace(referenceValue))
	if err != nil {
		return "", fmt.Errorf("ecosystem adapter: invalid endpoint: %w", err)
	}
	if reference.IsAbs() {
		if reference.User != nil || (reference.Scheme != "http" && reference.Scheme != "https") {
			return "", errors.New("ecosystem adapter: unsafe absolute endpoint")
		}
		if !strings.EqualFold(reference.Host, base.Host) && !strings.EqualFold(reference.Hostname(), "api.status.io") {
			return "", errors.New("ecosystem adapter: cross-origin endpoint is forbidden")
		}
		return reference.String(), nil
	}
	if reference.Host != "" || reference.User != nil {
		return "", errors.New("ecosystem adapter: endpoint authority is forbidden")
	}
	origin := *base
	origin.Path, origin.RawPath, origin.RawQuery, origin.Fragment = "/", "", "", ""
	return origin.ResolveReference(reference).String(), nil
}

func observedAt(response *transport.Response, fallback func() time.Time) time.Time {
	if !response.ObservedAt.IsZero() {
		return response.ObservedAt.UTC()
	}
	return fallback().UTC()
}

func cacheFromResponse(response *transport.Response) domain.CacheCapability {
	return domain.CacheCapability{SupportsETag: response.ETag != "", SupportsLastModified: response.LastModified != "",
		SupportsCacheControl: response.CacheControl != "", DefaultTTL: 15 * time.Second}
}

func metaFromResponse(endpoint string, response *transport.Response, observed time.Time) domain.FetchMeta {
	if response.FinalURL != "" {
		endpoint = response.FinalURL
	}
	var lastModified *time.Time
	if parsed, err := http.ParseTime(response.LastModified); err == nil {
		parsed = parsed.UTC()
		lastModified = &parsed
	}
	retryAfter := time.Duration(0)
	value := strings.TrimSpace(response.RetryAfter)
	if seconds, err := time.ParseDuration(value + "s"); err == nil && seconds > 0 {
		retryAfter = seconds
	} else if when, err := http.ParseTime(value); err == nil && when.After(observed) {
		retryAfter = when.Sub(observed)
	}
	contentType := ""
	if response.Header != nil {
		contentType = response.Header.Get("Content-Type")
	}
	return domain.FetchMeta{Endpoint: endpoint, StatusCode: response.StatusCode, ETag: response.ETag,
		LastModified: lastModified, ObservedAt: observed, NotModified: response.NotModified,
		ContentType: contentType, BodyBytes: int64(len(response.Body)), RetryAfter: retryAfter}
}

func expectedMediaType(engine string) string {
	if engine == EngineHTMLRecipe {
		return "text/html"
	}
	return "application/json"
}

func anyCache(endpoints map[domain.ResourceKind]domain.EndpointCapability, predicate func(domain.CacheCapability) bool) bool {
	for _, endpoint := range endpoints {
		if predicate(endpoint.Cache) {
			return true
		}
	}
	return false
}

func aggregateCompleteness(endpoints map[domain.ResourceKind]domain.EndpointCapability) domain.Completeness {
	for _, endpoint := range endpoints {
		if endpoint.Completeness != domain.CompletenessComplete {
			return domain.CompletenessPartial
		}
	}
	return domain.CompletenessComplete
}
