package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSlackRenderEscapesMentionsAndTruncatesUTF8Safely(t *testing.T) {
	t.Parallel()

	event := testCanonicalEvent()
	event.Subject = "Database <@U123>"
	event.Summary = "<!channel> " + strings.Repeat("服务降级", 300)
	endpoint := Endpoint{
		ID:              "slack-1",
		Channel:         ChannelSlack,
		URL:             "https://hooks.slack.com/services/T/B/secret",
		MaxPayloadBytes: 240,
	}
	payload, err := NewSlack(nil).Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(payload.Body) > endpoint.MaxPayloadBytes {
		t.Fatalf("payload bytes = %d, limit = %d", len(payload.Body), endpoint.MaxPayloadBytes)
	}
	if !payload.Degraded || !utf8.Valid(payload.Body) || !json.Valid(payload.Body) {
		t.Fatalf("invalid degraded Slack payload: %#v", payload)
	}
	var message struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload.Body, &message); err != nil {
		t.Fatalf("decode Slack body: %v", err)
	}
	if strings.Contains(message.Text, "<@") || strings.Contains(message.Text, "<!") {
		t.Fatalf("unsafe Slack mention remained in %q", message.Text)
	}
	if !strings.Contains(message.Text, "truncated") {
		t.Fatalf("truncation was not disclosed: %q", message.Text)
	}
}

func TestSlackTwoHundredMeansProviderAcceptedOnly(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	sender := HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.Header.Get("Content-Type") != SlackContentType {
			t.Fatalf("unexpected Slack request: %s %#v", request.Method, request.Header)
		}
		return response(http.StatusOK, nil, "ok"), nil
	})
	driver := NewSlack(sender, WithClock(func() time.Time { return fixedTime }))
	event := testCanonicalEvent()
	endpoint := Endpoint{ID: "slack-1", Channel: ChannelSlack, URL: "https://hooks.slack.com/services/T/B/secret"}
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery-s1", EventID: event.ID, Endpoint: endpoint}, payload)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if receipt.Status != StatusProviderAccepted || receipt.AcceptedAt != fixedTime {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestSlackClassifiesRetryAfterAndPermanentFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		status          int
		retryAfter      string
		wantClass       ErrorClass
		wantDelay       time.Duration
		disableEndpoint bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, retryAfter: "17", wantClass: ErrorClassRetryable, wantDelay: 17 * time.Second},
		{name: "request timeout", status: http.StatusRequestTimeout, wantClass: ErrorClassRetryable},
		{name: "too early", status: http.StatusTooEarly, wantClass: ErrorClassRetryable},
		{name: "server error", status: http.StatusBadGateway, wantClass: ErrorClassRetryable},
		{name: "bad request", status: http.StatusBadRequest, wantClass: ErrorClassPermanent},
		{name: "unauthorized", status: http.StatusUnauthorized, wantClass: ErrorClassPermanent},
		{name: "gone", status: http.StatusGone, wantClass: ErrorClassPermanent, disableEndpoint: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			headers := make(http.Header)
			headers.Set("Retry-After", test.retryAfter)
			driver := NewSlack(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
				return response(test.status, headers, "provider error"), nil
			}), WithClock(func() time.Time { return time.Unix(1_788_000_000, 0).UTC() }))
			event := testCanonicalEvent()
			endpoint := Endpoint{ID: "slack-1", Channel: ChannelSlack, URL: "https://hooks.slack.com/services/T/B/secret"}
			payload, err := driver.Render(context.Background(), event, endpoint)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			_, err = driver.Send(context.Background(), Delivery{ID: "delivery-s2", EventID: event.ID, Endpoint: endpoint}, payload)
			if err == nil {
				t.Fatal("expected send error")
			}
			decision := driver.Classify(err)
			if decision.Class != test.wantClass || decision.RetryAfter != test.wantDelay || decision.DisableEndpoint != test.disableEndpoint {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestSlackClassifiesNetworkFailureAsRetryableWithoutLeakingURL(t *testing.T) {
	t.Parallel()

	const secretURL = "https://hooks.slack.com/services/top-secret"
	driver := NewSlack(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed for " + secretURL)
	}))
	event := testCanonicalEvent()
	endpoint := Endpoint{ID: "slack-1", Channel: ChannelSlack, URL: secretURL}
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery-s3", EventID: event.ID, Endpoint: endpoint}, payload)
	if err == nil || driver.Classify(err).Class != ErrorClassRetryable {
		t.Fatalf("network error = %v", err)
	}
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("error leaked endpoint secret: %v", err)
	}
}

func TestHTTPDateRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	value := now.Add(45 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(value, now); got != 45*time.Second {
		t.Fatalf("Retry-After = %v", got)
	}
}

func TestClassifyErrorSentinels(t *testing.T) {
	t.Parallel()

	retryable := &Error{Class: ErrorClassRetryable}
	if !errors.Is(retryable, ErrRetryable) || errors.Is(retryable, ErrPermanent) {
		t.Fatal("retryable sentinel mismatch")
	}
	permanentError := &Error{Class: ErrorClassPermanent}
	if !errors.Is(permanentError, ErrPermanent) || errors.Is(permanentError, ErrRetryable) {
		t.Fatal("permanent sentinel mismatch")
	}
}
