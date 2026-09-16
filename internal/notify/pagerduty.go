package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const (
	PagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"
	pagerDutyMaxBytes  = 512 << 10
)

type PagerDuty struct {
	sender HTTPDoer
	now    clock
}

var _ ChannelDriver = (*PagerDuty)(nil)

func NewPagerDuty(sender HTTPDoer, options ...Option) *PagerDuty {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &PagerDuty{sender: sender, now: optionsClock(options)}
}

func (d *PagerDuty) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelPagerDuty, "validate endpoint", errors.New("context is nil"))
	}
	if endpoint.URL == "" {
		endpoint.URL = PagerDutyEventsURL
	}
	if err := validateEndpoint(endpoint, ChannelPagerDuty, false); err != nil {
		return err
	}
	if len(endpoint.Secret) == 0 {
		return permanent(ChannelPagerDuty, "validate endpoint", errors.New("routing key is required"))
	}
	return nil
}

func (d *PagerDuty) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if endpoint.URL == "" {
		endpoint.URL = PagerDutyEventsURL
	}
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelPagerDuty, "render", err)
	}
	action := "trigger"
	if event.Kind == "incident.resolved" || event.Kind == "maintenance.completed" || event.Kind == "source.recovered" {
		action = "resolve"
	}
	severity := pagerDutySeverity(event)
	summary := chatText(event)
	customDetails := make(map[string]any)
	if err := json.Unmarshal(event.Data, &customDetails); err != nil {
		customDetails["event"] = json.RawMessage(event.Data)
	}
	if event.ServiceName != "" {
		customDetails["service_name"] = event.ServiceName
	}
	if len(event.AffectedServices) > 0 {
		customDetails["affected_services"] = event.AffectedServices
	}
	body, err := json.Marshal(struct {
		RoutingKey  string `json:"routing_key"`
		EventAction string `json:"event_action"`
		DedupKey    string `json:"dedup_key"`
		Payload     any    `json:"payload,omitempty"`
	}{RoutingKey: string(endpoint.Secret), EventAction: action, DedupKey: pagerDutyDedupKey(event), Payload: map[string]any{
		"summary": summary, "source": event.Source, "severity": severity,
		"timestamp":      event.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		"custom_details": customDetails,
	}})
	if err != nil {
		return Payload{}, permanent(ChannelPagerDuty, "render", err)
	}
	limit := endpoint.MaxPayloadBytes
	if limit == 0 {
		limit = pagerDutyMaxBytes
	}
	if len(body) > limit {
		return Payload{}, permanent(ChannelPagerDuty, "render", errors.New("PagerDuty payload exceeds endpoint limit"))
	}
	return Payload{Channel: ChannelPagerDuty, EventID: event.ID, ContentType: "application/json", Body: body}, nil
}

func pagerDutyDedupKey(event CanonicalEvent) string {
	if strings.TrimSpace(event.EntityID) == "" {
		return string(event.ID)
	}
	value := event.Source + ":" + event.EntityID
	if len(value) <= 255 {
		return value
	}
	hash := sha256.Sum256([]byte(value))
	return "statushub:" + hex.EncodeToString(hash[:])
}

func (d *PagerDuty) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if delivery.Endpoint.URL == "" {
		delivery.Endpoint.URL = PagerDutyEventsURL
	}
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
	if err != nil {
		return Receipt{}, err
	}
	var response struct {
		DedupKey string `json:"dedup_key"`
	}
	_ = json.Unmarshal([]byte(receipt.ResponseBody), &response)
	if response.DedupKey == "" {
		response.DedupKey = string(delivery.EventID)
	}
	receipt.ProviderMessageID = response.DedupKey
	return receipt, nil
}

func (d *PagerDuty) Classify(err error) RetryDecision { return ClassifyError(err) }

func pagerDutySeverity(event CanonicalEvent) string {
	text := strings.ToLower(string(event.Data))
	switch {
	case strings.Contains(text, `"impact":"critical"`), strings.Contains(text, `"status":"major_outage"`):
		return "critical"
	case strings.Contains(text, `"impact":"major"`), strings.Contains(text, `"status":"partial_outage"`):
		return "error"
	case strings.Contains(text, `"impact":"minor"`), strings.Contains(text, `"status":"degraded"`):
		return "warning"
	default:
		return "info"
	}
}
