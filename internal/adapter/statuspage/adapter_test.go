package statuspage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/transport"
)

func TestNewRequiresInjectedTransport(t *testing.T) {
	t.Parallel()

	adapter, err := New(nil)
	if err == nil || adapter != nil {
		t.Fatalf("New(nil) = %#v, %v; want error", adapter, err)
	}
}

func TestProbeDiscoversSummaryAndUnresolvedIncidents(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch request.URL.Path {
		case SummaryPath:
			writer.Header().Set("ETag", `"summary-v1"`)
			writer.Header().Set("Last-Modified", "Wed, 10 Sep 2026 01:00:00 GMT")
			writer.Header().Set("Cache-Control", "public, max-age=30")
			_, _ = writer.Write([]byte(summaryFixture))
		case UnresolvedIncidentsPath:
			writer.Header().Set("ETag", `"incidents-v1"`)
			_, _ = writer.Write([]byte(unresolvedFixture))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	now := time.Date(2026, 9, 10, 4, 0, 0, 0, time.UTC)
	statusAdapter, err := New(httpTestClient{client: server.Client()}, WithClock(func() time.Time { return now }), WithCapabilityTTL(12*time.Hour))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	capabilities, err := statusAdapter.Probe(context.Background(), domain.Target{
		URL:      mustParseURL(t, server.URL+"/some/page"),
		SourceID: "source-1",
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if calls.Load() != 2 {
		t.Fatalf("probe calls = %d, want 2", calls.Load())
	}
	if capabilities.Engine != Engine || capabilities.Version != Version || capabilities.Confidence != 1 {
		t.Fatalf("identity/confidence = %q/%q/%v", capabilities.Engine, capabilities.Version, capabilities.Confidence)
	}
	if !capabilities.SupportsETag || !capabilities.SupportsModified {
		t.Fatalf("cache summary = ETag:%t Modified:%t", capabilities.SupportsETag, capabilities.SupportsModified)
	}
	if capabilities.SchemaHash == "" {
		t.Fatal("schema hash is empty")
	}
	if !capabilities.ExpiresAt.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("expires at = %v", capabilities.ExpiresAt)
	}

	summary, found := capabilities.Endpoints[domain.ResourceSummary]
	if !found {
		t.Fatal("summary endpoint was not discovered")
	}
	if summary.Path != SummaryPath || summary.Completeness != domain.CompletenessComplete {
		t.Fatalf("summary capability = %#v", summary)
	}
	if !summary.CanInferAbsence(domain.ResourceComponents) || !summary.CanInferAbsence(domain.ResourceUnresolvedIncidents) {
		t.Fatalf("summary authority = %#v", summary.AuthoritativeFor)
	}
	if summary.CanInferAbsence(domain.ResourceIncidents) {
		t.Fatal("summary must not claim authority over incident history")
	}
	if !summary.Cache.SupportsETag || !summary.Cache.SupportsLastModified || summary.Cache.DefaultTTL != 30*time.Second {
		t.Fatalf("summary cache capability = %#v", summary.Cache)
	}

	unresolved, found := capabilities.Endpoints[domain.ResourceUnresolvedIncidents]
	if !found {
		t.Fatal("unresolved incidents endpoint was not discovered")
	}
	if !unresolved.CanInferAbsence(domain.ResourceUnresolvedIncidents) {
		t.Fatalf("unresolved authority = %#v", unresolved.AuthoritativeFor)
	}
	if capabilities.Webhook.Supported {
		t.Fatal("probe must not infer an owner-configured public webhook")
	}
}

func TestProbeKeepsIndependentlyValidatedEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == SummaryPath {
			writer.Header().Set("Content-Type", "text/html")
			_, _ = writer.Write([]byte(`<html>not JSON</html>`))
			return
		}
		if request.URL.Path == UnresolvedIncidentsPath {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(unresolvedFixture))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()

	statusAdapter, err := New(httpTestClient{client: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	capabilities, err := statusAdapter.Probe(context.Background(), domain.Target{URL: mustParseURL(t, server.URL)})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if len(capabilities.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", capabilities.Endpoints)
	}
	if _, found := capabilities.Endpoints[domain.ResourceUnresolvedIncidents]; !found {
		t.Fatal("valid unresolved endpoint was discarded")
	}
	if capabilities.Confidence >= 0.98 {
		t.Fatalf("partial probe confidence = %v", capabilities.Confidence)
	}
}

func TestProbeRejectsUnrelatedJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	statusAdapter, err := New(httpTestClient{client: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = statusAdapter.Probe(context.Background(), domain.Target{URL: mustParseURL(t, server.URL)})
	if !errors.Is(err, ErrNotStatuspage) {
		t.Fatalf("Probe() error = %v, want ErrNotStatuspage", err)
	}
}

func TestProbeDoesNotMisclassifyTransportFailure(t *testing.T) {
	t.Parallel()

	transportFailure := errors.New("temporary network failure")
	statusAdapter, err := New(errorClient{err: transportFailure})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = statusAdapter.Probe(context.Background(), domain.Target{URL: mustParseURL(t, "https://status.example.com")})
	if !errors.Is(err, transportFailure) {
		t.Fatalf("Probe() error = %v, want transport failure", err)
	}
	if errors.Is(err, ErrNotStatuspage) {
		t.Fatalf("transport outage was misclassified as ErrNotStatuspage: %v", err)
	}
}

func TestFetchUsesConditionalsAndReturnsEvidence(t *testing.T) {
	t.Parallel()

	lastModified := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != UnresolvedIncidentsPath {
			t.Errorf("path = %q", request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		if got := request.Header.Get("If-None-Match"); got != `"previous"` {
			t.Errorf("If-None-Match = %q", got)
		}
		if got := request.Header.Get("If-Modified-Since"); got != lastModified.Format(http.TimeFormat) {
			t.Errorf("If-Modified-Since = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("ETag", `"current"`)
		writer.Header().Set("Last-Modified", "Wed, 10 Sep 2026 01:02:03 GMT")
		_, _ = writer.Write([]byte(unresolvedFixture))
	}))
	defer server.Close()

	now := time.Date(2026, 9, 10, 4, 5, 6, 0, time.UTC)
	statusAdapter, err := New(httpTestClient{client: server.Client()}, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, meta, err := statusAdapter.Fetch(context.Background(), domain.FetchRequest{
		Target:       domain.Target{URL: mustParseURL(t, server.URL+"/nested")},
		Source:       domain.Source{ID: "source-1"},
		ResourceKind: domain.ResourceUnresolvedIncidents,
		Endpoint: domain.EndpointCapability{
			Resource: domain.ResourceUnresolvedIncidents,
			Path:     UnresolvedIncidentsPath,
		},
		ETag:         `"previous"`,
		LastModified: &lastModified,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.ResourceKind != domain.ResourceUnresolvedIncidents || !snapshot.CanInferAbsence(domain.ResourceUnresolvedIncidents) {
		t.Fatalf("snapshot correctness metadata = %#v", snapshot)
	}
	if meta.StatusCode != http.StatusOK || meta.NotModified || meta.ETag != `"current"` {
		t.Fatalf("fetch metadata = %#v", meta)
	}
	if meta.LastModified == nil || !meta.LastModified.Equal(time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)) {
		t.Fatalf("last modified = %v", meta.LastModified)
	}
	if meta.Endpoint != server.URL+UnresolvedIncidentsPath || meta.ObservedAt != now {
		t.Fatalf("endpoint/observed at = %q/%v", meta.Endpoint, meta.ObservedAt)
	}
	if meta.SchemaHash == "" || meta.RawHash == "" || meta.BodyBytes != int64(len(unresolvedFixture)) {
		t.Fatalf("hash/size metadata = %#v", meta)
	}
}

func TestFetchHandlesNotModifiedWithoutDecoding(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("ETag", `"same"`)
		writer.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	statusAdapter, err := New(httpTestClient{client: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot, meta, err := statusAdapter.Fetch(context.Background(), domain.FetchRequest{
		Target:       domain.Target{URL: mustParseURL(t, server.URL)},
		ResourceKind: domain.ResourceSummary,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !meta.NotModified || meta.StatusCode != http.StatusNotModified {
		t.Fatalf("metadata = %#v", meta)
	}
	if snapshot.ResourceKind != domain.ResourceSummary || snapshot.Completeness != domain.CompletenessUnknown {
		t.Fatalf("unchanged snapshot = %#v", snapshot)
	}
}

func TestFetchRejectsUnsupportedOrCrossOriginEndpointBeforeNetwork(t *testing.T) {
	t.Parallel()

	client := &countingClient{}
	statusAdapter, err := New(client)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	target := domain.Target{URL: mustParseURL(t, "https://status.example.com")}

	_, _, err = statusAdapter.Fetch(context.Background(), domain.FetchRequest{
		Target:       target,
		ResourceKind: domain.ResourceStatus,
		Endpoint:     domain.EndpointCapability{Path: "/api/v2/status.json"},
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported resource error = %v", err)
	}

	_, _, err = statusAdapter.Fetch(context.Background(), domain.FetchRequest{
		Target:       target,
		ResourceKind: domain.ResourceSummary,
		Endpoint:     domain.EndpointCapability{Path: "https://attacker.example/steal"},
	})
	if err == nil {
		t.Fatal("cross-origin endpoint was accepted")
	}
	if client.calls.Load() != 0 {
		t.Fatalf("network calls = %d, want 0", client.calls.Load())
	}
}

func TestFetchClassifiesHTTPStatusAndRetryAfter(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "17")
		http.Error(writer, "busy", http.StatusTooManyRequests)
	}))
	defer server.Close()

	statusAdapter, err := New(httpTestClient{client: server.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, meta, err := statusAdapter.Fetch(context.Background(), domain.FetchRequest{
		Target:       domain.Target{URL: mustParseURL(t, server.URL)},
		ResourceKind: domain.ResourceSummary,
	})
	var statusError *HTTPStatusError
	if !errors.As(err, &statusError) {
		t.Fatalf("Fetch() error = %v, want HTTPStatusError", err)
	}
	if statusError.StatusCode != http.StatusTooManyRequests || statusError.RetryAfter != 17*time.Second {
		t.Fatalf("status error = %#v", statusError)
	}
	if meta.StatusCode != http.StatusTooManyRequests || meta.RetryAfter != 17*time.Second {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestDecodeWebhookIsExplicitlyUnsupported(t *testing.T) {
	t.Parallel()

	statusAdapter, err := New(&countingClient{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = statusAdapter.DecodeWebhook(context.Background(), domain.WebhookRequest{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("DecodeWebhook() error = %v", err)
	}
}

type httpTestClient struct {
	client *http.Client
}

func (c httpTestClient) Get(ctx context.Context, rawURL string, conditional transport.Conditional) (*transport.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if conditional.ETag != "" {
		request.Header.Set("If-None-Match", conditional.ETag)
	}
	if conditional.LastModified != "" {
		request.Header.Set("If-Modified-Since", conditional.LastModified)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return &transport.Response{
		StatusCode:   response.StatusCode,
		Header:       response.Header.Clone(),
		Body:         body,
		URL:          response.Request.URL.String(),
		ETag:         response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"),
		CacheControl: response.Header.Get("Cache-Control"),
		Age:          response.Header.Get("Age"),
		NotModified:  response.StatusCode == http.StatusNotModified,
	}, nil
}

type countingClient struct {
	calls atomic.Int32
}

type errorClient struct {
	err error
}

func (c errorClient) Get(context.Context, string, transport.Conditional) (*transport.Response, error) {
	return nil, c.err
}

func (c *countingClient) Get(context.Context, string, transport.Conditional) (*transport.Response, error) {
	c.calls.Add(1)
	return &transport.Response{StatusCode: http.StatusOK, Body: []byte(`{}`)}, nil
}

func TestMaxAge(t *testing.T) {
	t.Parallel()

	tests := map[string]time.Duration{
		"public, max-age=30": 30 * time.Second,
		"MAX-AGE=\"60\"":     60 * time.Second,
		"no-cache":           0,
		"max-age=invalid":    0,
		"max-age=-1":         0,
	}
	for input, expected := range tests {
		t.Run(strconv.Quote(input), func(t *testing.T) {
			t.Parallel()
			if actual := maxAge(input); actual != expected {
				t.Fatalf("maxAge(%q) = %v, want %v", input, actual, expected)
			}
		})
	}
}
