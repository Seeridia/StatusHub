package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SESMessage struct {
	From             string `json:"from"`
	To               string `json:"to"`
	Subject          string `json:"subject"`
	Text             string `json:"text"`
	ConfigurationSet string `json:"configuration_set,omitempty"`
	DeliveryID       string `json:"-"`
}

type SESAPI interface {
	SendEmail(context.Context, SESMessage) (string, error)
}

type SES struct{ client SESAPI }

var _ ChannelDriver = (*SES)(nil)

func NewSES(client SESAPI) *SES { return &SES{client: client} }

func (d *SES) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelEmailSES, "validate endpoint", errors.New("context is nil"))
	}
	if endpoint.Channel != ChannelEmailSES || strings.TrimSpace(endpoint.From) == "" || strings.TrimSpace(endpoint.To) == "" {
		return permanent(ChannelEmailSES, "validate endpoint", errors.New("email channel, from and to are required"))
	}
	if d == nil || d.client == nil {
		return permanent(ChannelEmailSES, "validate endpoint", errors.New("SES client is not configured"))
	}
	return nil
}

func (d *SES) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelEmailSES, "render", err)
	}
	summary := strings.TrimSpace(event.Summary)
	if summary == "" {
		summary = string(event.Data)
	}
	subject := strings.TrimSpace(event.Subject)
	if subject == "" {
		subject = string(event.Kind)
	}
	subject, _ = truncateRunes(subject, 200)
	summary, degraded := truncateRunes(summary, 20_000)
	body, err := json.Marshal(SESMessage{From: endpoint.From, To: endpoint.To, Subject: subject,
		Text: summary, ConfigurationSet: endpoint.ConfigurationSet})
	if err != nil {
		return Payload{}, permanent(ChannelEmailSES, "render", err)
	}
	return Payload{Channel: ChannelEmailSES, EventID: event.ID, ContentType: "application/json", Body: body, Degraded: degraded}, nil
}

func (d *SES) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	if payload.Channel != ChannelEmailSES || payload.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelEmailSES, "send", errors.New("payload was not rendered for SES"))
	}
	var message SESMessage
	if err := json.Unmarshal(payload.Body, &message); err != nil {
		return Receipt{}, permanent(ChannelEmailSES, "send", errors.New("invalid SES payload"))
	}
	message.DeliveryID = delivery.ID
	messageID, err := d.client.SendEmail(ctx, message)
	if err != nil {
		class := ErrorClassRetryable
		var coded interface{ ErrorCode() string }
		if errors.As(err, &coded) {
			switch coded.ErrorCode() {
			case "MessageRejected", "MailFromDomainNotVerifiedException", "AccountSuspendedException", "BadRequestException":
				class = ErrorClassPermanent
			}
		}
		return Receipt{}, &Error{Channel: ChannelEmailSES, Operation: "send", Class: class, ProviderCode: errorCode(err), Err: fmt.Errorf("SES request failed")}
	}
	return Receipt{Status: StatusProviderAccepted, AcceptedAt: time.Now().UTC(), ProviderMessageID: messageID, Degraded: payload.Degraded}, nil
}

func (d *SES) Classify(err error) RetryDecision { return ClassifyError(err) }

func errorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ""
}
