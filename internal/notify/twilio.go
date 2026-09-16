package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const twilioMaxMessageRunes = 1200

type TwilioSMS struct {
	sender HTTPDoer
	now    clock
}

var _ ChannelDriver = (*TwilioSMS)(nil)

func NewTwilioSMS(sender HTTPDoer, options ...Option) *TwilioSMS {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &TwilioSMS{sender: sender, now: optionsClock(options)}
}

func (d *TwilioSMS) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelTwilioSMS, "validate endpoint", errors.New("context is nil"))
	}
	if err := validateEndpoint(endpoint, ChannelTwilioSMS, false); err != nil {
		return err
	}
	if endpoint.AccountSID == "" || len(endpoint.Secret) == 0 || endpoint.From == "" || endpoint.To == "" {
		return permanent(ChannelTwilioSMS, "validate endpoint", errors.New("account SID, auth token, from and to are required"))
	}
	return nil
}

func (d *TwilioSMS) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelTwilioSMS, "render", err)
	}
	message := chatText(event)
	message, degraded := truncateRunes(message, twilioMaxMessageRunes)
	values := url.Values{"To": {endpoint.To}, "From": {endpoint.From}, "Body": {message}}
	if endpoint.CallbackURL != "" {
		values.Set("StatusCallback", endpoint.CallbackURL)
	}
	return Payload{Channel: ChannelTwilioSMS, EventID: event.ID,
		ContentType: "application/x-www-form-urlencoded", Body: []byte(values.Encode()), Degraded: degraded}, nil
}

func (d *TwilioSMS) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	decorate := func(request *http.Request, _ []byte, _ time.Time) error {
		request.SetBasicAuth(delivery.Endpoint.AccountSID, string(delivery.Endpoint.Secret))
		return nil
	}
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, decorate)
	if err != nil {
		return Receipt{}, err
	}
	var response struct {
		SID string `json:"sid"`
	}
	_ = json.Unmarshal([]byte(receipt.ResponseBody), &response)
	receipt.ProviderMessageID = response.SID
	return receipt, nil
}

func (d *TwilioSMS) Classify(err error) RetryDecision { return ClassifyError(err) }
