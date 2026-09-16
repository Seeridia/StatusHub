package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const defaultChatMaxPayloadBytes = 24 << 10

func chatText(event CanonicalEvent) string {
	text := strings.TrimSpace(event.Summary)
	if text == "" {
		text = string(event.Kind)
	}
	if subject := strings.TrimSpace(event.Subject); subject != "" && !strings.Contains(text, subject) {
		text = subject + "\n" + text
	}
	lines := make([]string, 0, 3)
	if service := strings.TrimSpace(event.ServiceName); service != "" {
		lines = append(lines, service)
	}
	lines = append(lines, text)
	if affected := affectedServicesText(event); affected != "" {
		lines = append(lines, "Affected services: "+affected)
	}
	return strings.Join(lines, "\n")
}

func eventTitle(event CanonicalEvent) string {
	title := strings.TrimSpace(event.Subject)
	if title == "" {
		title = string(event.Kind)
	}
	service := strings.TrimSpace(event.ServiceName)
	if service != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(service)) {
		title = service + " · " + title
	}
	return title
}

func affectedServicesText(event CanonicalEvent) string {
	seen := make(map[string]struct{}, len(event.AffectedServices))
	values := make([]string, 0, len(event.AffectedServices))
	for _, value := range event.AffectedServices {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, value)
	}
	return strings.Join(values, "、")
}

func marshalBounded(channel Channel, eventID domain.CanonicalEventID, maximum int, value any) (Payload, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return Payload{}, permanent(channel, "render", err)
	}
	if maximum == 0 {
		maximum = defaultChatMaxPayloadBytes
	}
	if len(body) > maximum {
		return Payload{}, permanent(channel, "render", fmt.Errorf("payload exceeds %d bytes", maximum))
	}
	return Payload{Channel: channel, EventID: eventID, ContentType: "application/json", Body: body}, nil
}

func businessError(channel Channel, code, message string, retryableCodes map[string]bool, disableCodes map[string]bool) error {
	class := ErrorClassPermanent
	if retryableCodes[code] {
		class = ErrorClassRetryable
	}
	return &Error{Channel: channel, Operation: "provider response", Class: class,
		ProviderCode: code, ResponseBody: truncateResponse(message), DisableEndpoint: disableCodes[code],
		Err: fmt.Errorf("provider code %s", code)}
}

func truncateResponse(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maximumResponsePreview {
		value = value[:maximumResponsePreview]
	}
	return value
}

type Teams struct {
	sender HTTPDoer
	now    clock
}

func NewTeams(sender HTTPDoer, options ...Option) *Teams {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &Teams{sender: sender, now: optionsClock(options)}
}

func (d *Teams) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelTeams, "validate endpoint", errors.New("context is nil"))
	}
	return validateEndpoint(endpoint, ChannelTeams, false)
}

func (d *Teams) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelTeams, "render", err)
	}
	value := map[string]any{"type": "message", "attachments": []any{map[string]any{
		"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil,
		"content": map[string]any{"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4",
			"body": []any{map[string]any{"type": "TextBlock", "weight": "Bolder", "text": eventTitle(event), "wrap": true},
				map[string]any{"type": "TextBlock", "text": chatText(event), "wrap": true}}},
	}}}
	return marshalBounded(ChannelTeams, event.ID, endpoint.MaxPayloadBytes, value)
}

func (d *Teams) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	if payload.Channel != ChannelTeams || payload.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelTeams, "send", errors.New("payload was not rendered for Teams"))
	}
	return sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
}
func (*Teams) Classify(err error) RetryDecision { return ClassifyError(err) }

type Discord struct {
	sender HTTPDoer
	now    clock
}

func NewDiscord(sender HTTPDoer, options ...Option) *Discord {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &Discord{sender: sender, now: optionsClock(options)}
}
func (d *Discord) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelDiscord, "validate endpoint", errors.New("context is nil"))
	}
	return validateEndpoint(endpoint, ChannelDiscord, false)
}
func (d *Discord) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelDiscord, "render", err)
	}
	text, degraded := truncateRunes(chatText(event), 1900)
	payload, err := marshalBounded(ChannelDiscord, event.ID, endpoint.MaxPayloadBytes,
		map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}})
	payload.Degraded = degraded
	return payload, err
}
func (d *Discord) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	parsed, err := url.Parse(delivery.Endpoint.URL)
	if err != nil {
		return Receipt{}, permanent(ChannelDiscord, "send", err)
	}
	query := parsed.Query()
	query.Set("wait", "true")
	parsed.RawQuery = query.Encode()
	delivery.Endpoint.URL = parsed.String()
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
	if err != nil {
		return Receipt{}, err
	}
	var response struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(receipt.ResponseBody), &response) == nil {
		receipt.ProviderMessageID = response.ID
	}
	return receipt, nil
}
func (*Discord) Classify(err error) RetryDecision { return ClassifyError(err) }

type Telegram struct {
	sender HTTPDoer
	now    clock
}

func NewTelegram(sender HTTPDoer, options ...Option) *Telegram {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &Telegram{sender: sender, now: optionsClock(options)}
}
func (d *Telegram) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelTelegram, "validate endpoint", errors.New("context is nil"))
	}
	if endpoint.URL == "" {
		endpoint.URL = "https://api.telegram.org"
	}
	if err := validateEndpoint(endpoint, ChannelTelegram, false); err != nil {
		return err
	}
	parsed, _ := url.Parse(endpoint.URL)
	if !strings.EqualFold(parsed.Hostname(), "api.telegram.org") {
		return permanent(ChannelTelegram, "validate endpoint", errors.New("Telegram endpoint host must be api.telegram.org"))
	}
	if !validTelegramToken(string(endpoint.Secret)) || strings.TrimSpace(endpoint.To) == "" {
		return permanent(ChannelTelegram, "validate endpoint", errors.New("bot token and chat ID are required"))
	}
	return nil
}
func (d *Telegram) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if endpoint.URL == "" {
		endpoint.URL = "https://api.telegram.org"
	}
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelTelegram, "render", err)
	}
	text, degraded := truncateRunes(chatText(event), 4000)
	payload, err := marshalBounded(ChannelTelegram, event.ID, endpoint.MaxPayloadBytes,
		map[string]any{"chat_id": endpoint.To, "text": text, "disable_web_page_preview": true})
	payload.Degraded = degraded
	return payload, err
}
func (d *Telegram) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if delivery.Endpoint.URL == "" {
		delivery.Endpoint.URL = "https://api.telegram.org"
	}
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	base, _ := url.Parse(delivery.Endpoint.URL)
	base.Path = "/bot" + string(delivery.Endpoint.Secret) + "/sendMessage"
	base.RawQuery, base.Fragment = "", ""
	delivery.Endpoint.URL = base.String()
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
	if err != nil {
		return Receipt{}, telegramHTTPError(err)
	}
	var response struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal([]byte(receipt.ResponseBody), &response) != nil || !response.OK {
		code := strconv.Itoa(response.ErrorCode)
		failure := businessError(ChannelTelegram, code, response.Description,
			map[string]bool{"429": true, "500": true, "502": true, "503": true},
			map[string]bool{"401": true, "403": true})
		if response.Parameters.RetryAfter > 0 {
			failure.(*Error).RetryAfter = time.Duration(response.Parameters.RetryAfter) * time.Second
		}
		return Receipt{}, failure
	}
	receipt.ProviderMessageID = strconv.FormatInt(response.Result.MessageID, 10)
	return receipt, nil
}
func (*Telegram) Classify(err error) RetryDecision { return ClassifyError(err) }

func telegramHTTPError(err error) error {
	var deliveryError *Error
	if !errors.As(err, &deliveryError) || deliveryError.ResponseBody == "" {
		return err
	}
	var response struct {
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal([]byte(deliveryError.ResponseBody), &response) == nil && response.Parameters.RetryAfter > 0 {
		clone := *deliveryError
		clone.RetryAfter = time.Duration(response.Parameters.RetryAfter) * time.Second
		return &clone
	}
	return err
}

func validTelegramToken(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || len(parts[0]) < 5 || len(parts[1]) < 20 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == ':' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
