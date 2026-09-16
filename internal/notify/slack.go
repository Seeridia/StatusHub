package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	SlackContentType                = "application/json"
	defaultSlackMaxPayloadBytes     = 32 << 10
	defaultSlackMaximumSummaryRunes = 3_500
	compactSlackMaximumRunes        = 320
)

type Slack struct {
	sender HTTPDoer
	now    clock
}

var _ ChannelDriver = (*Slack)(nil)

func NewSlack(sender HTTPDoer, options ...Option) *Slack {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &Slack{sender: sender, now: optionsClock(options)}
}

func (d *Slack) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelSlack, "validate endpoint", errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return &Error{Channel: ChannelSlack, Operation: "validate endpoint", Class: ErrorClassRetryable, Err: err}
	}
	return validateEndpoint(endpoint, ChannelSlack, false)
}

func (d *Slack) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelSlack, "render", err)
	}

	text := slackText(event)
	text, runeTruncated := truncateRunes(text, defaultSlackMaximumSummaryRunes)
	maximumBytes := endpoint.MaxPayloadBytes
	if maximumBytes == 0 {
		maximumBytes = defaultSlackMaxPayloadBytes
	}
	build := func(candidate string) ([]byte, error) {
		return json.Marshal(struct {
			Text string `json:"text"`
		}{Text: candidate})
	}
	body, byteTruncated, err := fitRenderedText(text, maximumBytes, build)
	if err != nil {
		return Payload{}, permanent(ChannelSlack, "render", err)
	}

	compactText, _ := truncateRunes(sanitizeSlackText(fmt.Sprintf("%s: %s (%s)", event.Kind, eventTitle(event), event.ID)), compactSlackMaximumRunes)
	fallback, _, err := fitRenderedText(compactText, maximumBytes, build)
	if err != nil {
		return Payload{}, permanent(ChannelSlack, "render fallback", err)
	}
	payload := Payload{
		Channel:     ChannelSlack,
		EventID:     event.ID,
		ContentType: SlackContentType,
		Body:        body,
		Degraded:    runeTruncated || byteTruncated,
	}
	if len(fallback) < len(body) {
		payload.FallbackBody = fallback
	}
	return payload, nil
}

func (d *Slack) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if d == nil {
		return Receipt{}, permanent(ChannelSlack, "send", errors.New("driver is nil"))
	}
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	if delivery.ID == "" || delivery.EventID == "" {
		return Receipt{}, permanent(ChannelSlack, "send", errors.New("delivery ID and event ID are required"))
	}
	if payload.Channel != ChannelSlack || payload.ContentType != SlackContentType || payload.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelSlack, "send", errors.New("payload was not rendered for Slack"))
	}
	return sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
}

func (d *Slack) Classify(err error) RetryDecision {
	return ClassifyError(err)
}

func slackText(event CanonicalEvent) string {
	// Prevent untrusted vendor text from creating Slack user, group, channel, or
	// everyone mentions. Ordinary Slack link markup remains available.
	return sanitizeSlackText(chatText(event))
}

func sanitizeSlackText(text string) string {
	text = strings.ReplaceAll(text, "<@", "&lt;@")
	return strings.ReplaceAll(text, "<!", "&lt;!")
}
