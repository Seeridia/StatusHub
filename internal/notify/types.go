// Package notify contains channel protocol drivers. Drivers render and send a
// single delivery attempt; queueing, retries, ordering, and the delivery ledger
// remain the caller's responsibility.
package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type Channel string

const (
	ChannelGenericWebhook Channel = "generic_webhook"
	ChannelSlack          Channel = "slack"
	ChannelSMTP           Channel = "smtp"
	ChannelEmailSES       Channel = "email_ses"
	ChannelPagerDuty      Channel = "pagerduty"
	ChannelTwilioSMS      Channel = "twilio_sms"
	ChannelTeams          Channel = "teams"
	ChannelDiscord        Channel = "discord"
	ChannelTelegram       Channel = "telegram"
	ChannelLark           Channel = "lark"
	ChannelDingTalk       Channel = "dingtalk"
	ChannelWeCom          Channel = "wecom"
	ChannelShoutrrr       Channel = "shoutrrr"
	ChannelPrivateAgent   Channel = "private_agent"
)

type DeliveryStatus string

const (
	// StatusProviderAccepted means only that the channel provider returned its
	// documented success response. It does not mean downstream delivery, display,
	// or human acknowledgement.
	StatusProviderAccepted DeliveryStatus = "provider_accepted"
)

type ErrorClass string

const (
	ErrorClassRetryable ErrorClass = "retryable"
	ErrorClassPermanent ErrorClass = "permanent"
)

// HTTPDoer is deliberately the same narrow contract implemented by
// http.Client, allowing policy-enforcing clients and unit-test fakes.
type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type HTTPDoerFunc func(request *http.Request) (*http.Response, error)

func (f HTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type Endpoint struct {
	ID               string  `json:"id"`
	Channel          Channel `json:"channel"`
	URL              string  `json:"-"`
	KeyID            string  `json:"key_id,omitempty"`
	Secret           []byte  `json:"-"`
	MaxPayloadBytes  int     `json:"max_payload_bytes,omitempty"`
	SMTPAddress      string  `json:"-"`
	SMTPUsername     string  `json:"-"`
	SMTPSecurity     string  `json:"smtp_security,omitempty"`
	AccountSID       string  `json:"-"`
	From             string  `json:"from,omitempty"`
	To               string  `json:"to,omitempty"`
	ConfigurationSet string  `json:"configuration_set,omitempty"`
	CallbackURL      string  `json:"-"`
}

// CanonicalEvent is the delivery-facing projection of a persisted canonical
// event. Data must contain the canonical JSON payload, not a raw observation.
type CanonicalEvent struct {
	ConsoleURL        string                  `json:"console_url,omitempty"`
	ConsoleIsIncident bool                    `json:"-"`
	ServiceName       string                  `json:"service_name,omitempty"`
	AffectedServices  []string                `json:"affected_services,omitempty"`
	ID                domain.CanonicalEventID `json:"id"`
	Source            string                  `json:"source"`
	Kind              domain.EventKind        `json:"kind"`
	Subject           string                  `json:"subject,omitempty"`
	EntityID          string                  `json:"entity_id,omitempty"`
	Time              time.Time               `json:"time"`
	Revision          uint64                  `json:"revision"`
	SchemaVersion     string                  `json:"schema_version"`
	Summary           string                  `json:"summary,omitempty"`
	Data              json.RawMessage         `json:"data"`
}

type Delivery struct {
	ID       string                  `json:"id"`
	EventID  domain.CanonicalEventID `json:"event_id"`
	Endpoint Endpoint                `json:"endpoint"`
}

// Payload contains a primary representation and, when useful, a smaller safe
// representation that may be attempted exactly once after HTTP 413.
type Payload struct {
	Channel      Channel
	EventID      domain.CanonicalEventID
	ContentType  string
	Body         []byte
	FallbackBody []byte
	Degraded     bool
}

type Receipt struct {
	Status            DeliveryStatus `json:"status"`
	HTTPStatus        int            `json:"http_status"`
	AcceptedAt        time.Time      `json:"accepted_at"`
	ResponseBody      string         `json:"response_body,omitempty"`
	Degraded          bool           `json:"degraded"`
	ProviderMessageID string         `json:"provider_message_id,omitempty"`
}

type RetryDecision struct {
	Class           ErrorClass
	RetryAfter      time.Duration
	DisableEndpoint bool
}

type ChannelDriver interface {
	Validate(ctx context.Context, endpoint Endpoint) error
	Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error)
	Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error)
	Classify(err error) RetryDecision
}
