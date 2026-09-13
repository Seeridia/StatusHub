package notify

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGenericWebhookStopsAfterOneCompact413(t *testing.T) {
	t.Parallel()

	event := testCanonicalEvent()
	event.Data = []byte(`{"message":"` + strings.Repeat("x", 4_000) + `"}`)
	event.Summary = strings.Repeat("summary", 100)
	endpoint := webhookEndpoint()
	calls := 0
	driver := NewGenericWebhook(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusRequestEntityTooLarge, nil, "too large"), nil
	}))
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(payload.FallbackBody) == 0 {
		t.Fatal("test requires a compact fallback")
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery-413", EventID: event.ID, Endpoint: endpoint}, payload)
	if err == nil {
		t.Fatal("expected the compact 413 to fail")
	}
	if calls != 2 {
		t.Fatalf("HTTP calls = %d, want exactly 2", calls)
	}
	decision := driver.Classify(err)
	if decision.Class != ErrorClassPermanent {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestCompactAttemptCanReturnRetryableRateLimit(t *testing.T) {
	t.Parallel()

	event := testCanonicalEvent()
	event.Data = []byte(`{"message":"` + strings.Repeat("x", 4_000) + `"}`)
	event.Summary = strings.Repeat("summary", 100)
	endpoint := webhookEndpoint()
	calls := 0
	driver := NewGenericWebhook(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(http.StatusRequestEntityTooLarge, nil, "too large"), nil
		}
		headers := make(http.Header)
		headers.Set("Retry-After", "23")
		return response(http.StatusTooManyRequests, headers, "slow down"), nil
	}))
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery-429", EventID: event.ID, Endpoint: endpoint}, payload)
	if err == nil {
		t.Fatal("expected rate-limit failure")
	}
	decision := driver.Classify(err)
	if decision.Class != ErrorClassRetryable || decision.RetryAfter != 23*time.Second {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestServerFailureIsNotRetriedInsideDriver(t *testing.T) {
	t.Parallel()

	calls := 0
	driver := NewSlack(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(http.StatusServiceUnavailable, nil, "unavailable"), nil
	}))
	event := testCanonicalEvent()
	endpoint := Endpoint{ID: "slack-1", Channel: ChannelSlack, URL: "https://hooks.slack.com/services/T/B/secret"}
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery-no-retry", EventID: event.ID, Endpoint: endpoint}, payload)
	if err == nil || driver.Classify(err).Class != ErrorClassRetryable {
		t.Fatalf("send error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("HTTP calls = %d, driver must leave retries to its caller", calls)
	}
}

func TestEndpointValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint Endpoint
	}{
		{name: "wrong channel", endpoint: Endpoint{Channel: ChannelSlack, URL: "https://example.test", KeyID: "key", Secret: []byte("secret")}},
		{name: "plain HTTP", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "http://example.test", KeyID: "key", Secret: []byte("secret")}},
		{name: "userinfo", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "https://user:secret@example.test", KeyID: "key", Secret: []byte("secret")}},
		{name: "fragment", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "https://example.test/#secret", KeyID: "key", Secret: []byte("secret")}},
		{name: "missing key ID", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "https://example.test", Secret: []byte("secret")}},
		{name: "invalid key ID", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "https://example.test", KeyID: "key,other", Secret: []byte("secret")}},
		{name: "missing secret", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "https://example.test", KeyID: "key"}},
		{name: "negative body limit", endpoint: Endpoint{Channel: ChannelGenericWebhook, URL: "https://example.test", KeyID: "key", Secret: []byte("secret"), MaxPayloadBytes: -1}},
	}
	driver := NewGenericWebhook(nil)
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := driver.Validate(context.Background(), test.endpoint)
			if err == nil || driver.Classify(err).Class != ErrorClassPermanent {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestDefaultSenderRejectsPrivateTargetWithoutConnecting(t *testing.T) {
	t.Parallel()

	driver := NewGenericWebhook(nil)
	event := testCanonicalEvent()
	endpoint := webhookEndpoint()
	endpoint.URL = "https://127.0.0.1/hooks"
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery-private", EventID: event.ID, Endpoint: endpoint}, payload)
	if err == nil {
		t.Fatal("Send() error = nil, want unsafe-target rejection")
	}
	if decision := driver.Classify(err); decision.Class != ErrorClassPermanent {
		t.Fatalf("unsafe target decision = %#v, error = %v", decision, err)
	}
}

func TestTooSmallPayloadLimitIsPermanent(t *testing.T) {
	t.Parallel()

	endpoint := webhookEndpoint()
	endpoint.MaxPayloadBytes = 16
	driver := NewGenericWebhook(nil)
	_, err := driver.Render(context.Background(), testCanonicalEvent(), endpoint)
	if err == nil || driver.Classify(err).Class != ErrorClassPermanent {
		t.Fatalf("render error = %v", err)
	}
}

func TestCanceledContextIsRetryable(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	driver := NewSlack(nil)
	endpoint := Endpoint{Channel: ChannelSlack, URL: "https://hooks.slack.com/services/T/B/secret"}
	err := driver.Validate(ctx, endpoint)
	if !errors.Is(err, context.Canceled) || driver.Classify(err).Class != ErrorClassRetryable {
		t.Fatalf("canceled validation = %v", err)
	}
}

func TestRetryAfterParsingBounds(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		value string
		want  time.Duration
	}{
		{value: "", want: 0},
		{value: "invalid", want: 0},
		{value: "-3", want: 0},
		{value: now.Add(-time.Second).Format(http.TimeFormat), want: 0},
		{value: "9223372036854775807", want: time.Duration(math.MaxInt64)},
	}
	for _, test := range tests {
		if got := parseRetryAfter(test.value, now); got != test.want {
			t.Fatalf("parseRetryAfter(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestErrorResponsePreviewIsBounded(t *testing.T) {
	t.Parallel()

	driver := NewSlack(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest, nil, strings.Repeat("x", maximumResponsePreview+100)), nil
	}))
	event := testCanonicalEvent()
	endpoint := Endpoint{ID: "slack-1", Channel: ChannelSlack, URL: "https://hooks.slack.com/services/T/B/secret"}
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery-preview", EventID: event.ID, Endpoint: endpoint}, payload)
	var deliveryError *Error
	if !errors.As(err, &deliveryError) {
		t.Fatalf("send error = %v", err)
	}
	if len(deliveryError.ResponseBody) != maximumResponsePreview {
		t.Fatalf("response preview length = %d", len(deliveryError.ResponseBody))
	}
}
