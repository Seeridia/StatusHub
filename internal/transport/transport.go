// Package transport provides a bounded HTTP GET client for fetching public
// vendor status endpoints. It validates and pins DNS answers on every request
// and redirect hop to prevent SSRF and DNS-rebinding bypasses.
package transport

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultConnectTimeout              = 2 * time.Second
	defaultTLSHandshakeTimeout         = 3 * time.Second
	defaultResponseHeaderTimeout       = 5 * time.Second
	defaultRequestTimeout              = 10 * time.Second
	defaultIdleConnTimeout             = 90 * time.Second
	defaultMaxBodyBytes          int64 = 2 << 20
	defaultMaxRedirects                = 3
	defaultMaxHeaderBytes              = 1 << 20
)

// Doer is implemented by http.Client and keeps callers easy to fake in unit
// tests without coupling them to the concrete safe client.
type Doer interface {
	Do(request *http.Request) (*http.Response, error)
}

// Client is the narrow interface consumed by source adapters.
type Client interface {
	Get(ctx context.Context, rawURL string, conditional Conditional) (*Response, error)
}

// Conditional supplies HTTP validators from the previous successful fetch.
type Conditional struct {
	ETag         string
	LastModified string
}

// Response contains a fully-read, decompressed, size-bounded response body.
// Header is a clone and remains valid after Get closes the wire response.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	// FinalURL is the URL after all accepted redirects. URL is retained as a
	// compatibility alias and has the same value.
	FinalURL     string
	URL          string
	ETag         string
	LastModified string
	CacheControl string
	Age          string
	RetryAfter   string
	ObservedAt   time.Time
	NotModified  bool
}

// Config controls resource and network bounds. Zero duration and size values
// use DefaultConfig values. Set DisableRedirects to reject the first redirect.
type Config struct {
	Resolver Resolver
	Dialer   Dialer

	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	RequestTimeout        time.Duration
	IdleConnTimeout       time.Duration

	MaxBodyBytes           int64
	MaxRedirects           int
	DisableRedirects       bool
	MaxResponseHeaderBytes int64
	MaxIdleConns           int
	MaxIdleConnsPerHost    int
	MaxConnsPerHost        int

	UserAgent  string
	TLSRootCAs *x509.CertPool
}

// DefaultConfig returns production-safe defaults suitable for ordinary JSON
// status endpoints.
func DefaultConfig() Config {
	return Config{
		ConnectTimeout:         defaultConnectTimeout,
		TLSHandshakeTimeout:    defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout:  defaultResponseHeaderTimeout,
		RequestTimeout:         defaultRequestTimeout,
		IdleConnTimeout:        defaultIdleConnTimeout,
		MaxBodyBytes:           defaultMaxBodyBytes,
		MaxRedirects:           defaultMaxRedirects,
		MaxResponseHeaderBytes: defaultMaxHeaderBytes,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    8,
		MaxConnsPerHost:        16,
		UserAgent:              "vendor-status-monitoring/1",
	}
}

// HTTPClient is a safe concrete implementation of Client.
type HTTPClient struct {
	client       *http.Client
	transport    *http.Transport
	maxBodyBytes int64
	userAgent    string
}

var _ Client = (*HTTPClient)(nil)
var _ Doer = (*HTTPClient)(nil)

// New constructs an isolated HTTP client. It deliberately ignores environment
// proxy variables because a proxy would bypass the validated-IP dial path.
func New(config Config) (*HTTPClient, error) {
	config, err := normalizedConfig(config)
	if err != nil {
		return nil, err
	}

	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	baseDialer := config.Dialer
	if baseDialer == nil {
		baseDialer = &net.Dialer{
			Timeout:   config.ConnectTimeout,
			KeepAlive: 30 * time.Second,
		}
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.TLSRootCAs != nil {
		tlsConfig.RootCAs = config.TLSRootCAs.Clone()
	}
	baseTransport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&pinnedDialer{base: baseDialer, connectTimeout: config.ConnectTimeout}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           config.MaxIdleConns,
		MaxIdleConnsPerHost:    config.MaxIdleConnsPerHost,
		MaxConnsPerHost:        config.MaxConnsPerHost,
		IdleConnTimeout:        config.IdleConnTimeout,
		TLSHandshakeTimeout:    config.TLSHandshakeTimeout,
		ResponseHeaderTimeout:  config.ResponseHeaderTimeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: config.MaxResponseHeaderBytes,
		DisableCompression:     true,
		TLSClientConfig:        tlsConfig,
	}
	policy := &targetPolicy{resolver: resolver}
	secureTransport := &policyRoundTripper{base: baseTransport, policy: policy}

	maxRedirects := config.MaxRedirects
	if config.DisableRedirects {
		maxRedirects = 0
	}
	httpClient := &http.Client{
		Transport: secureTransport,
		Timeout:   config.RequestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return &Error{
					Kind: KindTooManyRedirects,
					Op:   "redirect",
					URL:  redactedURL(request.URL),
					Err:  fmt.Errorf("limit is %d", maxRedirects),
				}
			}
			// Do not disclose the source URL or any credentials to another origin.
			request.Header.Del("Referer")
			if len(via) > 0 && !sameOrigin(via[len(via)-1].URL, request.URL) {
				request.Header.Del("Authorization")
				request.Header.Del("Cookie")
				request.Header.Del("Proxy-Authorization")
			}
			return nil
		},
	}

	return &HTTPClient{
		client:       httpClient,
		transport:    baseTransport,
		maxBodyBytes: config.MaxBodyBytes,
		userAgent:    config.UserAgent,
	}, nil
}

// Do exposes the same policy-enforcing client for bounded callers such as
// outbound notification drivers. The caller owns and must close the response
// body. URL validation, DNS pinning, redirect policy, timeouts, and header
// limits are still enforced; callers are responsible for bounding body reads.
func (c *HTTPClient) Do(request *http.Request) (*http.Response, error) {
	if c == nil || c.client == nil {
		return nil, &Error{Kind: KindTransport, Op: "request", Err: errors.New("client is nil")}
	}
	if request == nil {
		return nil, &Error{Kind: KindInvalidURL, Op: "request", Err: errors.New("request is nil")}
	}
	response, err := c.client.Do(request)
	if err != nil {
		return response, classifyRequestError(err, request.URL)
	}
	return response, nil
}

// Get fetches rawURL with optional validators. HTTP status codes, including
// 304, are returned as Response values rather than converted into errors.
func (c *HTTPClient) Get(ctx context.Context, rawURL string, conditional Conditional) (*Response, error) {
	if c == nil || c.client == nil {
		return nil, &Error{Kind: KindTransport, Op: "GET", Err: errors.New("client is nil")}
	}
	if ctx == nil {
		return nil, &Error{Kind: KindInvalidURL, Op: "GET", Err: errors.New("context is nil")}
	}
	if !validHeaderValue(conditional.ETag) || !validHeaderValue(conditional.LastModified) {
		return nil, &Error{Kind: KindInvalidURL, Op: "GET", Err: errors.New("conditional header contains an invalid byte")}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &Error{Kind: KindInvalidURL, Op: "create request", Err: err}
	}
	if conditional.ETag != "" {
		request.Header.Set("If-None-Match", conditional.ETag)
	}
	if conditional.LastModified != "" {
		request.Header.Set("If-Modified-Since", conditional.LastModified)
	}
	if c.userAgent != "" {
		request.Header.Set("User-Agent", c.userAgent)
	}
	request.Header.Set("Accept-Encoding", "gzip")

	wireResponse, err := c.client.Do(request)
	if err != nil {
		if wireResponse != nil && wireResponse.Body != nil {
			_ = wireResponse.Body.Close()
		}
		return nil, classifyRequestError(err, request.URL)
	}
	defer wireResponse.Body.Close()
	observedAt := time.Now().UTC()

	responseURL := request.URL
	if wireResponse.Request != nil && wireResponse.Request.URL != nil {
		responseURL = wireResponse.Request.URL
	}
	headers := wireResponse.Header.Clone()

	reader, closeDecoder, decodeErrorKind, err := decodedReader(wireResponse)
	if err != nil {
		return nil, &Error{Kind: decodeErrorKind, Op: "decode body", URL: redactedURL(responseURL), Err: err}
	}
	if closeDecoder != nil {
		defer closeDecoder()
	}

	body, err := io.ReadAll(io.LimitReader(reader, c.maxBodyBytes+1))
	if err != nil {
		return nil, classifyBodyError(err, responseURL)
	}
	if int64(len(body)) > c.maxBodyBytes {
		return nil, &Error{
			Kind: KindBodyTooLarge,
			Op:   "read body",
			URL:  redactedURL(responseURL),
			Err:  fmt.Errorf("decompressed body exceeds %d bytes", c.maxBodyBytes),
		}
	}

	return &Response{
		StatusCode:   wireResponse.StatusCode,
		Header:       headers,
		Body:         body,
		FinalURL:     responseURL.String(),
		URL:          responseURL.String(),
		ETag:         headers.Get("ETag"),
		LastModified: headers.Get("Last-Modified"),
		CacheControl: headers.Get("Cache-Control"),
		Age:          headers.Get("Age"),
		RetryAfter:   headers.Get("Retry-After"),
		ObservedAt:   observedAt,
		NotModified:  wireResponse.StatusCode == http.StatusNotModified,
	}, nil
}

// CloseIdleConnections releases pooled sockets owned by this client.
func (c *HTTPClient) CloseIdleConnections() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

type policyRoundTripper struct {
	base   *http.Transport
	policy *targetPolicy
}

func (t *policyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, &Error{Kind: KindInvalidURL, Op: "round trip", Err: errors.New("request is nil")}
	}
	plan, err := t.policy.resolve(request.Context(), request.URL)
	if err != nil {
		return nil, err
	}
	requestCopy := request.Clone(context.WithValue(request.Context(), planContextKey{}, plan))
	// An alternate Host header can decouple HTTP routing from the URL that was
	// validated. SafeClient never sets one; defensively remove it here.
	requestCopy.Host = ""
	return t.base.RoundTrip(requestCopy)
}

func normalizedConfig(config Config) (Config, error) {
	defaults := DefaultConfig()
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = defaults.ConnectTimeout
	}
	if config.TLSHandshakeTimeout == 0 {
		config.TLSHandshakeTimeout = defaults.TLSHandshakeTimeout
	}
	if config.ResponseHeaderTimeout == 0 {
		config.ResponseHeaderTimeout = defaults.ResponseHeaderTimeout
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = defaults.RequestTimeout
	}
	if config.IdleConnTimeout == 0 {
		config.IdleConnTimeout = defaults.IdleConnTimeout
	}
	if config.MaxBodyBytes == 0 {
		config.MaxBodyBytes = defaults.MaxBodyBytes
	}
	if config.MaxRedirects == 0 {
		config.MaxRedirects = defaults.MaxRedirects
	}
	if config.MaxResponseHeaderBytes == 0 {
		config.MaxResponseHeaderBytes = defaults.MaxResponseHeaderBytes
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = defaults.MaxIdleConns
	}
	if config.MaxIdleConnsPerHost == 0 {
		config.MaxIdleConnsPerHost = defaults.MaxIdleConnsPerHost
	}
	if config.MaxConnsPerHost == 0 {
		config.MaxConnsPerHost = defaults.MaxConnsPerHost
	}
	if config.UserAgent == "" {
		config.UserAgent = defaults.UserAgent
	}

	if config.ConnectTimeout < 0 || config.TLSHandshakeTimeout < 0 || config.ResponseHeaderTimeout < 0 || config.RequestTimeout < 0 || config.IdleConnTimeout < 0 {
		return Config{}, &Error{Kind: KindInvalidURL, Op: "configure", Err: errors.New("timeouts cannot be negative")}
	}
	if config.MaxBodyBytes < 1 || config.MaxBodyBytes == math.MaxInt64 {
		return Config{}, &Error{Kind: KindInvalidURL, Op: "configure", Err: errors.New("MaxBodyBytes must be between 1 and MaxInt64-1")}
	}
	if config.MaxRedirects < 0 || config.MaxResponseHeaderBytes < 0 || config.MaxIdleConns < 0 || config.MaxIdleConnsPerHost < 0 || config.MaxConnsPerHost < 0 {
		return Config{}, &Error{Kind: KindInvalidURL, Op: "configure", Err: errors.New("limits cannot be negative")}
	}
	if !validHeaderValue(config.UserAgent) {
		return Config{}, &Error{Kind: KindInvalidURL, Op: "configure", Err: errors.New("UserAgent contains an invalid byte")}
	}
	return config, nil
}

func decodedReader(response *http.Response) (io.Reader, func(), ErrorKind, error) {
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
		return response.Body, nil, "", nil
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, nil, KindReadBody, err
		}
		return reader, func() { _ = reader.Close() }, "", nil
	default:
		return nil, nil, KindUnsupportedEncoding, fmt.Errorf("content encoding %q is not supported", encoding)
	}
}

func classifyRequestError(err error, target *url.URL) error {
	var transportError *Error
	if errors.As(err, &transportError) {
		// http.Client wraps RoundTripper and redirect errors in url.Error, whose
		// URL can contain a sensitive query. Return our already-redacted error.
		return transportError
	}
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		err = urlError.Err
	}
	kind := KindTransport
	if errors.Is(err, context.Canceled) {
		kind = KindCanceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		kind = KindTimeout
	} else {
		var networkError net.Error
		var operationError *net.OpError
		var certificateError *tls.CertificateVerificationError
		var hostnameError x509.HostnameError
		var authorityError x509.UnknownAuthorityError
		switch {
		case errors.As(err, &networkError) && networkError.Timeout():
			kind = KindTimeout
		case errors.As(err, &certificateError), errors.As(err, &hostnameError), errors.As(err, &authorityError):
			kind = KindTLS
		case errors.As(err, &operationError) && operationError.Op == "dial":
			kind = KindConnect
		}
	}
	return &Error{Kind: kind, Op: "GET", URL: redactedURL(target), Err: err}
}

func classifyBodyError(err error, target *url.URL) error {
	kind := KindReadBody
	if errors.Is(err, context.Canceled) {
		kind = KindCanceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		kind = KindTimeout
	} else {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			kind = KindTimeout
		}
	}
	return &Error{Kind: kind, Op: "read body", URL: redactedURL(target), Err: err}
}

func validHeaderValue(value string) bool {
	for index := range len(value) {
		character := value[index]
		if character == '\t' || (character >= 0x20 && character != 0x7f) {
			continue
		}
		return false
	}
	return true
}

func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
