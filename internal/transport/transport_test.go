package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	publicIPv4A = netip.MustParseAddr("93.184.216.34")
	publicIPv4B = netip.MustParseAddr("1.1.1.1")
)

type staticResolver struct {
	mu      sync.Mutex
	records map[string][]netip.Addr
	err     error
	calls   []string
}

func (r *staticResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, network+":"+host)
	if r.err != nil {
		return nil, r.err
	}
	return append([]netip.Addr(nil), r.records[host]...), nil
}

func (r *staticResolver) callCount(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if call == "ip:"+host {
			count++
		}
	}
	return count
}

type loopbackDialer struct {
	mu        sync.Mutex
	requested []string
}

func (d *loopbackDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.requested = append(d.requested, address)
	d.mu.Unlock()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
}

func (d *loopbackDialer) addresses() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.requested...)
}

type rejectingDialer struct {
	calls atomic.Int64
}

func (d *rejectingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	return nil, errors.New("dial should not have been called")
}

type blockingDialer struct{}

func (blockingDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestGetConditionalCompressedResponseAndMetadata(t *testing.T) {
	lastModified := "Wed, 10 Sep 2026 01:02:03 GMT"
	body := []byte(`{"page":{"status":"ok"}}`)
	var expectedHost string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.Host != expectedHost {
			t.Errorf("Host = %q, want %q", request.Host, expectedHost)
		}
		if got := request.Header.Get("If-None-Match"); got != `"old"` {
			t.Errorf("If-None-Match = %q", got)
		}
		if got := request.Header.Get("If-Modified-Since"); got != lastModified {
			t.Errorf("If-Modified-Since = %q", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Errorf("Accept-Encoding = %q, want gzip", got)
		}

		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("ETag", `"new"`)
		writer.Header().Set("Last-Modified", lastModified)
		writer.Header().Set("Cache-Control", "public, max-age=30")
		writer.Header().Set("Age", "7")
		writer.Header().Set("Retry-After", "12")
		compressed := gzip.NewWriter(writer)
		_, _ = compressed.Write(body)
		_ = compressed.Close()
	}))
	defer server.Close()

	endpoint := publicServerURL(t, server.URL, "status.example")
	expectedHost = mustParseURL(t, endpoint).Host
	resolver := &staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}}
	dialer := &loopbackDialer{}
	client := testClient(t, resolver, dialer, nil)
	defer client.CloseIdleConnections()

	before := time.Now().UTC()
	response, err := client.Get(context.Background(), endpoint, Conditional{ETag: `"old"`, LastModified: lastModified})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(response.Body, body) {
		t.Fatalf("Body = %q, want %q", response.Body, body)
	}
	if response.StatusCode != http.StatusOK || response.NotModified {
		t.Fatalf("status = %d, notModified = %v", response.StatusCode, response.NotModified)
	}
	if response.ETag != `"new"` || response.LastModified != lastModified {
		t.Fatalf("validators = (%q, %q)", response.ETag, response.LastModified)
	}
	if response.CacheControl != "public, max-age=30" || response.Age != "7" || response.RetryAfter != "12" {
		t.Fatalf("cache metadata = (%q, %q, %q)", response.CacheControl, response.Age, response.RetryAfter)
	}
	if response.FinalURL != endpoint || response.URL != endpoint {
		t.Fatalf("final URLs = (%q, %q), want %q", response.FinalURL, response.URL, endpoint)
	}
	if response.ObservedAt.Before(before) || response.ObservedAt.After(time.Now().UTC()) {
		t.Fatalf("ObservedAt = %s, outside request interval", response.ObservedAt)
	}
	if got := dialer.addresses(); len(got) != 1 || !strings.HasPrefix(got[0], publicIPv4A.String()+":") {
		t.Fatalf("dialed addresses = %v; want verified IP %s", got, publicIPv4A)
	}
}

func TestGetNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("ETag", `"same"`)
		writer.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{}, nil)

	response, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !response.NotModified || response.StatusCode != http.StatusNotModified || len(response.Body) != 0 || response.ETag != `"same"` {
		t.Fatalf("unexpected 304 response: %#v", response)
	}
}

func TestBodyLimitAppliesAfterDecompression(t *testing.T) {
	for _, test := range []struct {
		name       string
		compressed bool
	}{
		{name: "identity"},
		{name: "gzip", compressed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				payload := bytes.Repeat([]byte("a"), 128)
				if !test.compressed {
					_, _ = writer.Write(payload)
					return
				}
				writer.Header().Set("Content-Encoding", "gzip")
				compressed := gzip.NewWriter(writer)
				_, _ = compressed.Write(payload)
				_ = compressed.Close()
			}))
			defer server.Close()

			client := testClient(t,
				&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
				&loopbackDialer{},
				func(config *Config) { config.MaxBodyBytes = 32 })
			_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
			if !errors.Is(err, ErrBodyTooLarge) {
				t.Fatalf("Get() error = %v, want ErrBodyTooLarge", err)
			}
			if kind, ok := KindOf(err); !ok || kind != KindBodyTooLarge {
				t.Fatalf("KindOf(error) = (%q, %v)", kind, ok)
			}
		})
	}
}

func TestUnsupportedContentEncoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "br")
		_, _ = writer.Write([]byte("encoded"))
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{}, nil)

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if !errors.Is(err, ErrUnsupportedEncoding) {
		t.Fatalf("Get() error = %v, want ErrUnsupportedEncoding", err)
	}
}

func TestMalformedGzipIsAReadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		_, _ = writer.Write([]byte("not a gzip stream"))
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{}, nil)

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if !errors.Is(err, ErrReadBody) {
		t.Fatalf("Get() error = %v, want ErrReadBody", err)
	}
}

func TestUnsafeLiteralAndLocalTargetsAreRejectedBeforeDial(t *testing.T) {
	targets := []string{
		"http://localhost/",
		"http://api.localhost/",
		"http://localhost.localdomain/",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.0.1/",
		"http://169.254.1.1/",
		"http://169.254.169.254/",
		"http://100.64.0.1/",
		"http://100.100.100.200/",
		"http://192.0.0.192/",
		"http://192.0.2.1/",
		"http://198.18.0.1/",
		"http://203.0.113.1/",
		"http://168.63.129.16/",
		"http://224.0.0.1/",
		"http://255.255.255.255/",
		"http://0.0.0.0/",
		"http://[::]/",
		"http://[::7f00:1]/",
		"http://[::1]/",
		"http://[fc00::1]/",
		"http://[fe80::1]/",
		"http://[ff02::1]/",
		"http://[::ffff:8.8.8.8]/",
		"http://[64:ff9b::808:808]/",
		"http://[2001:db8::1]/",
		"http://[fd00:ec2::254]/",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			dialer := &rejectingDialer{}
			client := testClient(t, &staticResolver{}, dialer, nil)
			_, err := client.Get(context.Background(), target, Conditional{})
			if !errors.Is(err, ErrUnsafeTarget) {
				t.Fatalf("Get(%q) error = %v, want ErrUnsafeTarget", target, err)
			}
			if dialer.calls.Load() != 0 {
				t.Fatalf("dial called %d times", dialer.calls.Load())
			}
		})
	}
}

func TestAnyUnsafeDNSAnswerRejectsTheWholeSet(t *testing.T) {
	dialer := &rejectingDialer{}
	resolver := &staticResolver{records: map[string][]netip.Addr{
		"mixed.example": {publicIPv4A, netip.MustParseAddr("127.0.0.1")},
	}}
	client := testClient(t, resolver, dialer, nil)

	_, err := client.Get(context.Background(), "http://mixed.example/", Conditional{})
	if !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("Get() error = %v, want ErrUnsafeTarget", err)
	}
	if dialer.calls.Load() != 0 {
		t.Fatalf("dial called %d times", dialer.calls.Load())
	}
}

func TestInvalidURLsAndSensitiveValuesAreRejected(t *testing.T) {
	client := testClient(t, &staticResolver{}, &rejectingDialer{}, nil)

	_, err := client.Get(context.Background(), "ftp://status.example/file", Conditional{})
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("ftp error = %v, want ErrUnsupportedScheme", err)
	}

	_, err = client.Get(context.Background(), "https://alice:do-not-log@status.example/path?token=also-secret", Conditional{})
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("userinfo error = %v, want ErrInvalidURL", err)
	}
	if strings.Contains(err.Error(), "do-not-log") || strings.Contains(err.Error(), "also-secret") {
		t.Fatalf("error leaked sensitive URL data: %v", err)
	}

	_, err = client.Get(context.Background(), "https://status.example/", Conditional{ETag: "ok\r\nInjected: yes"})
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("header error = %v, want ErrInvalidURL", err)
	}
}

func TestRedirectsAreRevalidatedAndFinalURLIsReported(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/start":
			target := publicServerURL(t, server.URL, "second.example") + "/final"
			http.Redirect(writer, request, target, http.StatusFound)
		case "/final":
			_, _ = writer.Write([]byte("done"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	resolver := &staticResolver{records: map[string][]netip.Addr{
		"first.example":  {publicIPv4A},
		"second.example": {publicIPv4B},
	}}
	client := testClient(t, resolver, &loopbackDialer{}, nil)
	start := publicServerURL(t, server.URL, "first.example") + "/start"
	wantFinal := publicServerURL(t, server.URL, "second.example") + "/final"

	response, err := client.Get(context.Background(), start, Conditional{})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(response.Body) != "done" || response.FinalURL != wantFinal {
		t.Fatalf("response = body %q, final URL %q", response.Body, response.FinalURL)
	}
	if resolver.callCount("first.example") != 1 || resolver.callCount("second.example") != 1 {
		t.Fatalf("resolver calls = %v", resolver.calls)
	}
}

func TestRedirectToUnsafeTargetIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, port, _ := net.SplitHostPort(request.Host)
		http.Redirect(writer, request, "http://127.0.0.1:"+port+"/private", http.StatusFound)
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{}, nil)

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("Get() error = %v, want ErrUnsafeTarget", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var current int
		_, _ = fmt.Sscanf(request.URL.Path, "/%d", &current)
		http.Redirect(writer, request, fmt.Sprintf("/%d", current+1), http.StatusFound)
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{},
		func(config *Config) { config.MaxRedirects = 2 })

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example")+"/0", Conditional{})
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("Get() error = %v, want ErrTooManyRedirects", err)
	}
}

func TestDisableRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/next", http.StatusFound)
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{},
		func(config *Config) { config.DisableRedirects = true })

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("Get() error = %v, want ErrTooManyRedirects", err)
	}
}

func TestDNSFailureClassification(t *testing.T) {
	resolverError := &net.DNSError{Err: "no such host", Name: "missing.example", IsNotFound: true}
	client := testClient(t, &staticResolver{err: resolverError}, &rejectingDialer{}, nil)

	_, err := client.Get(context.Background(), "https://missing.example/status", Conditional{})
	if !errors.Is(err, ErrDNS) {
		t.Fatalf("Get() error = %v, want ErrDNS", err)
	}
}

func TestConnectionTimeout(t *testing.T) {
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		blockingDialer{},
		func(config *Config) {
			config.ConnectTimeout = 30 * time.Millisecond
			config.RequestTimeout = time.Second
		})

	_, err := client.Get(context.Background(), "http://status.example/", Conditional{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Get() error = %v, want ErrTimeout", err)
	}
}

func TestResponseHeaderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{},
		func(config *Config) {
			config.ResponseHeaderTimeout = 30 * time.Millisecond
			config.RequestTimeout = time.Second
		})

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Get() error = %v, want ErrTimeout", err)
	}
}

func TestTotalTimeoutWhileReadingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("partial"))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"status.example": {publicIPv4A}}},
		&loopbackDialer{},
		func(config *Config) {
			config.ResponseHeaderTimeout = time.Second
			config.RequestTimeout = 40 * time.Millisecond
		})

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "status.example"), Conditional{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Get() error = %v, want ErrTimeout", err)
	}
}

func TestPinnedDialPreservesTLSHostnameAndHTTPHost(t *testing.T) {
	var gotHost atomic.Value
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotHost.Store(request.Host)
		_, _ = writer.Write([]byte("secure"))
	}))
	defer server.Close()

	certificatePool := x509.NewCertPool()
	certificatePool.AddCert(server.Certificate())
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"example.com": {publicIPv4A}}},
		&loopbackDialer{},
		func(config *Config) { config.TLSRootCAs = certificatePool })
	endpoint := publicServerURL(t, server.URL, "example.com")

	response, err := client.Get(context.Background(), endpoint, Conditional{})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(response.Body) != "secure" {
		t.Fatalf("Body = %q", response.Body)
	}
	if got, _ := gotHost.Load().(string); got != mustParseURL(t, endpoint).Host {
		t.Fatalf("HTTP Host = %q, want %q", got, mustParseURL(t, endpoint).Host)
	}
	// httptest's certificate is valid for example.com. A successful handshake
	// therefore proves certificate verification used the original hostname,
	// not the pinned TEST-NET address or loopback test socket.
}

func TestTLSVerificationFailureClassification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("unexpected"))
	}))
	defer server.Close()

	certificatePool := x509.NewCertPool()
	certificatePool.AddCert(server.Certificate())
	client := testClient(t,
		&staticResolver{records: map[string][]netip.Addr{"wrong-host.example": {publicIPv4A}}},
		&loopbackDialer{},
		func(config *Config) { config.TLSRootCAs = certificatePool })

	_, err := client.Get(context.Background(), publicServerURL(t, server.URL, "wrong-host.example"), Conditional{})
	if !errors.Is(err, ErrTLS) {
		t.Fatalf("Get() error = %v, want ErrTLS", err)
	}
}

func testClient(t *testing.T, resolver Resolver, dialer Dialer, modify func(*Config)) *HTTPClient {
	t.Helper()
	config := DefaultConfig()
	config.Resolver = resolver
	config.Dialer = dialer
	config.ConnectTimeout = time.Second
	config.TLSHandshakeTimeout = time.Second
	config.ResponseHeaderTimeout = time.Second
	config.RequestTimeout = 2 * time.Second
	if modify != nil {
		modify(&config)
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func publicServerURL(t *testing.T, serverURL, hostname string) string {
	t.Helper()
	parsed := mustParseURL(t, serverURL)
	parsed.Host = net.JoinHostPort(hostname, parsed.Port())
	return parsed.String()
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	return parsed
}
