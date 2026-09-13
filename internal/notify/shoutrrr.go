package notify

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/containrrr/shoutrrr"
)

// ShoutrrrSender is injectable so the bridge can be contract-tested without
// contacting a third party. The durable ledger and retry policy remain in the
// notifier worker; this interface performs one attempt only.
type ShoutrrrSender interface {
	Send(context.Context, string, string) error
}

type shoutrrrLibrary struct{}

func (shoutrrrLibrary) Send(ctx context.Context, serviceURL, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return shoutrrr.Send(serviceURL, message)
}

type Shoutrrr struct {
	sender ShoutrrrSender
	now    clock
}

func NewShoutrrr(sender ShoutrrrSender, options ...Option) *Shoutrrr {
	if sender == nil {
		sender = shoutrrrLibrary{}
	}
	return &Shoutrrr{sender: sender, now: optionsClock(options)}
}

func (d *Shoutrrr) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelShoutrrr, "validate endpoint", errors.New("context is nil"))
	}
	if endpoint.Channel != ChannelShoutrrr {
		return permanent(ChannelShoutrrr, "validate endpoint", errors.New("channel must be shoutrrr"))
	}
	if len(endpoint.URL) == 0 || len(endpoint.URL) > 8192 || strings.ContainsAny(endpoint.URL, "\r\n") {
		return permanent(ChannelShoutrrr, "validate endpoint", errors.New("service URL is required and must be bounded"))
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil || parsed.Scheme == "" {
		return permanent(ChannelShoutrrr, "validate endpoint", errors.New("invalid Shoutrrr service URL"))
	}
	// Native drivers have stricter provider-response parsing and must not be
	// bypassed through the long-tail bridge.
	switch strings.ToLower(parsed.Scheme) {
	case "slack", "discord", "telegram", "smtp", "teams", "generic", "generic+http", "generic+https":
		return permanent(ChannelShoutrrr, "validate endpoint", errors.New("service is handled by a native channel driver"))
	}
	if endpoint.MaxPayloadBytes < 0 {
		return permanent(ChannelShoutrrr, "validate endpoint", errors.New("maximum payload bytes cannot be negative"))
	}
	return nil
}

func (d *Shoutrrr) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelShoutrrr, "render", err)
	}
	text, degraded := truncateRunes(chatText(event), 3900)
	limit := endpoint.MaxPayloadBytes
	if limit == 0 {
		limit = 16 << 10
	}
	if len(text) > limit {
		return Payload{}, permanent(ChannelShoutrrr, "render", errors.New("message exceeds endpoint limit"))
	}
	return Payload{Channel: ChannelShoutrrr, EventID: event.ID, ContentType: "text/plain; charset=utf-8", Body: []byte(text), Degraded: degraded}, nil
}

func (d *Shoutrrr) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if d == nil || d.sender == nil {
		return Receipt{}, permanent(ChannelShoutrrr, "send", errors.New("driver is not initialized"))
	}
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	if delivery.ID == "" || delivery.EventID == "" || payload.Channel != ChannelShoutrrr || payload.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelShoutrrr, "send", errors.New("delivery identity or payload is invalid"))
	}
	if err := d.sender.Send(ctx, delivery.Endpoint.URL, string(payload.Body)); err != nil {
		// Shoutrrr errors may echo its credential-bearing service URI. Keep the
		// original out of logs and the durable delivery attempt summary.
		return Receipt{}, &Error{Channel: ChannelShoutrrr, Operation: "send", Class: ErrorClassRetryable,
			Err: errors.New("provider attempt failed; details withheld")}
	}
	return Receipt{Status: StatusProviderAccepted, AcceptedAt: d.now(), Degraded: payload.Degraded}, nil
}

func (*Shoutrrr) Classify(err error) RetryDecision { return ClassifyError(err) }

var _ ChannelDriver = (*Shoutrrr)(nil)
