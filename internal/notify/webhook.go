package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	CloudEventsContentType = "application/cloudevents+json"
	CloudEventsSpecVersion = "1.0"

	HeaderDeliveryID         = "X-Delivery-ID"
	HeaderEventID            = "X-Event-ID"
	HeaderSignature          = "X-Signature"
	HeaderSignatureTimestamp = "X-Signature-Timestamp"

	defaultWebhookMaxPayloadBytes = 256 << 10
)

type GenericWebhook struct {
	sender HTTPDoer
	now    clock
}

var _ ChannelDriver = (*GenericWebhook)(nil)

func NewGenericWebhook(sender HTTPDoer, options ...Option) *GenericWebhook {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &GenericWebhook{sender: sender, now: optionsClock(options)}
}

func (d *GenericWebhook) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelGenericWebhook, "validate endpoint", errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return &Error{Channel: ChannelGenericWebhook, Operation: "validate endpoint", Class: ErrorClassRetryable, Err: err}
	}
	return validateEndpoint(endpoint, ChannelGenericWebhook, true)
}

func (d *GenericWebhook) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelGenericWebhook, "render", err)
	}

	full, err := marshalCloudEvent(event, event.Data, false)
	if err != nil {
		return Payload{}, permanent(ChannelGenericWebhook, "render", err)
	}
	limit := endpoint.MaxPayloadBytes
	if limit == 0 {
		limit = defaultWebhookMaxPayloadBytes
	}
	payload := Payload{Channel: ChannelGenericWebhook, EventID: event.ID, ContentType: CloudEventsContentType}
	if len(full) <= limit {
		payload.Body = full
		compact, compactErr := compactCloudEvent(event, limit)
		if compactErr == nil && len(compact) < len(full) {
			payload.FallbackBody = compact
		}
		return payload, nil
	}
	compact, err := compactCloudEvent(event, limit)
	if err != nil {
		return Payload{}, permanent(ChannelGenericWebhook, "render fallback", err)
	}
	payload.Body = compact
	payload.Degraded = true
	return payload, nil
}

func (d *GenericWebhook) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if d == nil {
		return Receipt{}, permanent(ChannelGenericWebhook, "send", errors.New("driver is nil"))
	}
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	if delivery.ID == "" || delivery.EventID == "" {
		return Receipt{}, permanent(ChannelGenericWebhook, "send", errors.New("delivery ID and event ID are required"))
	}
	if payload.Channel != ChannelGenericWebhook || payload.ContentType != CloudEventsContentType || payload.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelGenericWebhook, "send", errors.New("payload was not rendered for generic webhook"))
	}

	decorate := func(request *http.Request, body []byte, attemptTime time.Time) error {
		timestamp := strconv.FormatInt(attemptTime.Unix(), 10)
		signature := ComputeWebhookSignature(delivery.Endpoint.Secret, timestamp, body)
		request.Header.Set(HeaderDeliveryID, delivery.ID)
		request.Header.Set(HeaderEventID, string(delivery.EventID))
		request.Header.Set(HeaderSignatureTimestamp, timestamp)
		request.Header.Set(HeaderSignature, fmt.Sprintf(
			"v1,kid=%s,t=%s,sig=%s", delivery.Endpoint.KeyID, timestamp, signature,
		))
		return nil
	}
	return sendHTTP(ctx, d.sender, d.now, delivery, payload, decorate)
}

func (d *GenericWebhook) Classify(err error) RetryDecision {
	return ClassifyError(err)
}

// ComputeWebhookSignature signs timestamp + "." + the exact transmitted body.
func ComputeWebhookSignature(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature uses constant-time comparison and is suitable for
// receivers after they have selected the secret identified by kid.
func VerifyWebhookSignature(secret []byte, timestamp string, body []byte, hexadecimalSignature string) bool {
	provided, err := hex.DecodeString(hexadecimalSignature)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(ComputeWebhookSignature(secret, timestamp, body))
	if err != nil {
		return false
	}
	return hmac.Equal(expected, provided)
}

type cloudEventEnvelope struct {
	SpecVersion       string          `json:"specversion"`
	ID                string          `json:"id"`
	Source            string          `json:"source"`
	Type              string          `json:"type"`
	Subject           string          `json:"subject,omitempty"`
	Time              string          `json:"time"`
	DataContentType   string          `json:"datacontenttype"`
	DataSchema        string          `json:"dataschema,omitempty"`
	SchemaVersion     string          `json:"schemaversion"`
	Revision          uint64          `json:"revision"`
	DataTruncated     bool            `json:"datatruncated,omitempty"`
	OriginalDataBytes int             `json:"originaldatabytes,omitempty"`
	OriginalDataHash  string          `json:"originaldatasha256,omitempty"`
	Data              json.RawMessage `json:"data"`
}

type compactWebhookData struct {
	Summary   string `json:"summary,omitempty"`
	Truncated bool   `json:"truncated"`
}

func validateCanonicalEvent(event CanonicalEvent) error {
	if event.ID == "" {
		return errors.New("event ID is required")
	}
	if !event.Kind.Valid() {
		return errors.New("valid event kind is required")
	}
	if event.Revision == 0 {
		return errors.New("positive event revision is required")
	}
	if strings.TrimSpace(event.SchemaVersion) == "" {
		return errors.New("schema version is required")
	}
	if event.Time.IsZero() {
		return errors.New("event time is required")
	}
	if strings.TrimSpace(event.Source) == "" {
		return errors.New("event source is required")
	}
	if _, err := url.Parse(event.Source); err != nil {
		return fmt.Errorf("parse event source: %w", err)
	}
	if len(event.Data) == 0 || !json.Valid(event.Data) {
		return errors.New("event data must be valid JSON")
	}
	return nil
}

func marshalCloudEvent(event CanonicalEvent, data json.RawMessage, truncated bool) ([]byte, error) {
	envelope := cloudEventEnvelope{
		SpecVersion:     CloudEventsSpecVersion,
		ID:              string(event.ID),
		Source:          event.Source,
		Type:            string(event.Kind),
		Subject:         event.Subject,
		Time:            event.Time.UTC().Format(time.RFC3339Nano),
		DataContentType: "application/json",
		DataSchema:      schemaURI(event.SchemaVersion),
		SchemaVersion:   event.SchemaVersion,
		Revision:        event.Revision,
		DataTruncated:   truncated,
		Data:            data,
	}
	if truncated {
		envelope.OriginalDataBytes = len(event.Data)
		sum := sha256.Sum256(event.Data)
		envelope.OriginalDataHash = hex.EncodeToString(sum[:])
	}
	return json.Marshal(envelope)
}

func compactCloudEvent(event CanonicalEvent, maximumBytes int) ([]byte, error) {
	if maximumBytes <= 0 {
		return nil, errors.New("maximum payload bytes must be positive")
	}
	summary := strings.TrimSpace(event.Summary)
	if summary == "" {
		summary = strings.TrimSpace(string(event.Data))
	}

	build := func(candidate string) ([]byte, error) {
		compactData, err := json.Marshal(compactWebhookData{Summary: candidate, Truncated: true})
		if err != nil {
			return nil, err
		}
		return marshalCloudEvent(event, compactData, true)
	}
	body, _, err := fitRenderedText(summary, maximumBytes, build)
	return body, err
}

func schemaURI(version string) string {
	return "urn:vendor-status-monitoring:schema:" + url.PathEscape(version)
}
