package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

func TestGenericWebhookRendersCloudEvents10(t *testing.T) {
	t.Parallel()

	driver := NewGenericWebhook(nil)
	payload, err := driver.Render(context.Background(), testCanonicalEvent(), webhookEndpoint())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if payload.Channel != ChannelGenericWebhook || payload.ContentType != CloudEventsContentType {
		t.Fatalf("unexpected payload metadata: %#v", payload)
	}
	if payload.EventID != testCanonicalEvent().ID {
		t.Fatalf("payload event ID = %q", payload.EventID)
	}

	var envelope map[string]any
	if err := json.Unmarshal(payload.Body, &envelope); err != nil {
		t.Fatalf("decode CloudEvent: %v", err)
	}
	assertJSONField(t, envelope, "specversion", CloudEventsSpecVersion)
	assertJSONField(t, envelope, "id", string(testCanonicalEvent().ID))
	assertJSONField(t, envelope, "source", testCanonicalEvent().Source)
	assertJSONField(t, envelope, "type", string(testCanonicalEvent().Kind))
	assertJSONField(t, envelope, "datacontenttype", "application/json")
	assertJSONField(t, envelope, "schemaversion", testCanonicalEvent().SchemaVersion)
	if envelope["data"] == nil {
		t.Fatal("CloudEvent data is missing")
	}
}

func TestGenericWebhookSignsExactBodyAndReturnsProviderAccepted(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 9, 10, 8, 30, 0, 0, time.UTC)
	var capturedBody []byte
	var capturedHeader http.Header
	sender := HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		var err error
		capturedBody, err = io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		capturedHeader = request.Header.Clone()
		return response(http.StatusAccepted, nil, "accepted"), nil
	})
	driver := NewGenericWebhook(sender, WithClock(func() time.Time { return fixedTime }))
	event := testCanonicalEvent()
	endpoint := webhookEndpoint()
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	delivery := Delivery{ID: "delivery-1", EventID: event.ID, Endpoint: endpoint}
	receipt, err := driver.Send(context.Background(), delivery, payload)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if receipt.Status != StatusProviderAccepted || receipt.HTTPStatus != http.StatusAccepted {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	if receipt.AcceptedAt != fixedTime {
		t.Fatalf("accepted at = %v, want %v", receipt.AcceptedAt, fixedTime)
	}
	if !bytes.Equal(capturedBody, payload.Body) {
		t.Fatal("signature must cover the exact transmitted body")
	}
	if got := capturedHeader.Get(HeaderDeliveryID); got != delivery.ID {
		t.Fatalf("delivery header = %q", got)
	}
	if got := capturedHeader.Get(HeaderEventID); got != string(event.ID) {
		t.Fatalf("event header = %q", got)
	}
	timestamp := capturedHeader.Get(HeaderSignatureTimestamp)
	if timestamp != "1789029000" {
		t.Fatalf("signature timestamp = %q", timestamp)
	}
	wantSignature := ComputeWebhookSignature(endpoint.Secret, timestamp, capturedBody)
	wantHeader := "v1,kid=" + endpoint.KeyID + ",t=" + timestamp + ",sig=" + wantSignature
	if got := capturedHeader.Get(HeaderSignature); got != wantHeader {
		t.Fatalf("signature header = %q, want %q", got, wantHeader)
	}
	if !VerifyWebhookSignature(endpoint.Secret, timestamp, capturedBody, wantSignature) {
		t.Fatal("signature did not verify")
	}
	if VerifyWebhookSignature([]byte("wrong"), timestamp, capturedBody, wantSignature) {
		t.Fatal("signature verified with the wrong key")
	}
}

func TestWebhookSignatureKnownVector(t *testing.T) {
	t.Parallel()

	const want = "28739ce20e2d3d0eb7847a31bca889409d78898aa78efb82e124f99ddd0818a2"
	if got := ComputeWebhookSignature([]byte("key"), "123", []byte("{}")); got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

func TestGenericWebhookRetriesOnceWithSignedCompactPayloadAfter413(t *testing.T) {
	t.Parallel()

	event := testCanonicalEvent()
	event.Data = json.RawMessage(`{"detail":"` + strings.Repeat("x", 8_000) + `"}`)
	event.Summary = strings.Repeat("summary ", 100)
	endpoint := webhookEndpoint()

	var bodies [][]byte
	var signatures []string
	var timestamps []string
	sender := HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		bodies = append(bodies, body)
		signatures = append(signatures, request.Header.Get(HeaderSignature))
		timestamps = append(timestamps, request.Header.Get(HeaderSignatureTimestamp))
		if len(bodies) == 1 {
			return response(http.StatusRequestEntityTooLarge, nil, "too large"), nil
		}
		return response(http.StatusNoContent, nil, ""), nil
	})
	clockCalls := 0
	driver := NewGenericWebhook(sender, WithClock(func() time.Time {
		value := time.Unix(1_788_000_000+int64(clockCalls), 0).UTC()
		clockCalls++
		return value
	}))
	payload, err := driver.Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(payload.FallbackBody) == 0 || len(payload.FallbackBody) >= len(payload.Body) {
		t.Fatalf("expected smaller fallback: primary=%d fallback=%d", len(payload.Body), len(payload.FallbackBody))
	}

	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery-2", EventID: event.ID, Endpoint: endpoint}, payload)
	if err != nil {
		t.Fatalf("send with fallback: %v", err)
	}
	if !receipt.Degraded || receipt.Status != StatusProviderAccepted {
		t.Fatalf("unexpected fallback receipt: %#v", receipt)
	}
	if len(bodies) != 2 || bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("requests = %d; expected two distinct bodies", len(bodies))
	}
	if signatures[0] == signatures[1] {
		t.Fatal("fallback body must have a distinct signature")
	}
	if timestamps[0] == timestamps[1] {
		t.Fatal("each HTTP attempt must generate a fresh signature timestamp")
	}
	var compact map[string]any
	if err := json.Unmarshal(bodies[1], &compact); err != nil {
		t.Fatalf("decode compact CloudEvent: %v", err)
	}
	if compact["datatruncated"] != true {
		t.Fatalf("compact event did not declare truncation: %#v", compact)
	}
}

func TestGenericWebhookPreemptivelyBoundsLargePayload(t *testing.T) {
	t.Parallel()

	event := testCanonicalEvent()
	event.Data = json.RawMessage(`{"detail":"` + strings.Repeat("界", 2_000) + `"}`)
	event.Summary = strings.Repeat("状态更新", 100)
	endpoint := webhookEndpoint()
	endpoint.MaxPayloadBytes = 620

	payload, err := NewGenericWebhook(nil).Render(context.Background(), event, endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(payload.Body) > endpoint.MaxPayloadBytes {
		t.Fatalf("payload bytes = %d, limit = %d", len(payload.Body), endpoint.MaxPayloadBytes)
	}
	if !payload.Degraded {
		t.Fatal("bounded payload should be marked degraded")
	}
	if !json.Valid(payload.Body) {
		t.Fatal("bounded CloudEvent is not valid JSON")
	}
}

func TestGenericWebhookRejectsUnsafeEndpointAndMismatchedEvent(t *testing.T) {
	t.Parallel()

	driver := NewGenericWebhook(nil)
	endpoint := webhookEndpoint()
	endpoint.URL = "http://hooks.example.test/events"
	if decision := driver.Classify(driver.Validate(context.Background(), endpoint)); decision.Class != ErrorClassPermanent {
		t.Fatalf("unsafe endpoint decision = %#v", decision)
	}

	endpoint = webhookEndpoint()
	payload, err := driver.Render(context.Background(), testCanonicalEvent(), endpoint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	_, err = driver.Send(context.Background(), Delivery{
		ID:       "delivery-3",
		EventID:  domain.CanonicalEventID("another-event"),
		Endpoint: endpoint,
	}, payload)
	if err == nil || driver.Classify(err).Class != ErrorClassPermanent {
		t.Fatalf("mismatched event error = %v", err)
	}
}

func testCanonicalEvent() CanonicalEvent {
	return CanonicalEvent{
		ID:            domain.CanonicalEventID("01993ce8-4d00-7000-8000-000000000001"),
		Source:        "urn:statushub:source:github-status",
		Kind:          domain.EventKindIncidentUpdated,
		Subject:       "incident/api-degradation",
		Time:          time.Date(2026, 9, 10, 8, 29, 0, 123, time.UTC),
		Revision:      2,
		SchemaVersion: "canonical/v1",
		Summary:       "API performance is degraded",
		Data:          json.RawMessage(`{"incident":{"id":"incident-1","phase":"monitoring","impact":"minor"}}`),
	}
}

func webhookEndpoint() Endpoint {
	return Endpoint{
		ID:      "endpoint-1",
		Channel: ChannelGenericWebhook,
		URL:     "https://hooks.example.test/events",
		KeyID:   "key_2026_09",
		Secret:  []byte("a-test-secret"),
	}
}

func response(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertJSONField(t *testing.T, object map[string]any, key string, want any) {
	t.Helper()
	if got := object[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}
