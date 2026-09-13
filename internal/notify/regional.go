package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Lark struct {
	sender HTTPDoer
	now    clock
}

func NewLark(sender HTTPDoer, options ...Option) *Lark {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &Lark{sender: sender, now: optionsClock(options)}
}
func (d *Lark) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelLark, "validate endpoint", errors.New("context is nil"))
	}
	if err := validateEndpoint(endpoint, ChannelLark, false); err != nil {
		return err
	}
	if len(endpoint.Secret) == 0 {
		return permanent(ChannelLark, "validate endpoint", errors.New("webhook signing secret is required"))
	}
	return nil
}
func (d *Lark) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelLark, "render", err)
	}
	timestamp := strconv.FormatInt(d.now().Unix(), 10)
	signature := larkSignature(timestamp, endpoint.Secret)
	text, degraded := truncateRunes(chatText(event), 3900)
	payload, err := marshalBounded(ChannelLark, event.ID, endpoint.MaxPayloadBytes, map[string]any{
		"timestamp": timestamp, "sign": signature, "msg_type": "text", "content": map[string]string{"text": text},
	})
	payload.Degraded = degraded
	return payload, err
}
func (d *Lark) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	if payload.Channel != ChannelLark || payload.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelLark, "send", errors.New("payload was not rendered for Lark"))
	}
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
	if err != nil {
		return Receipt{}, err
	}
	var response struct {
		Code          int    `json:"code"`
		StatusCode    int    `json:"StatusCode"`
		Message       string `json:"msg"`
		StatusMessage string `json:"StatusMessage"`
	}
	if err := json.Unmarshal([]byte(receipt.ResponseBody), &response); err != nil {
		return Receipt{}, permanent(ChannelLark, "provider response", errors.New("invalid JSON response"))
	}
	code := response.Code
	if code == 0 {
		code = response.StatusCode
	}
	if code != 0 {
		message := firstResponse(response.Message, response.StatusMessage)
		return Receipt{}, businessError(ChannelLark, strconv.Itoa(code), message,
			map[string]bool{"9499": true, "99991663": true}, map[string]bool{"19021": true})
	}
	return receipt, nil
}
func (*Lark) Classify(err error) RetryDecision { return ClassifyError(err) }

func larkSignature(timestamp string, secret []byte) string {
	key := []byte(timestamp + "\n" + string(secret))
	mac := hmac.New(sha256.New, key)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type DingTalk struct {
	sender HTTPDoer
	now    clock
}

func NewDingTalk(sender HTTPDoer, options ...Option) *DingTalk {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &DingTalk{sender: sender, now: optionsClock(options)}
}
func (d *DingTalk) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelDingTalk, "validate endpoint", errors.New("context is nil"))
	}
	if err := validateEndpoint(endpoint, ChannelDingTalk, false); err != nil {
		return err
	}
	if len(endpoint.Secret) == 0 {
		return permanent(ChannelDingTalk, "validate endpoint", errors.New("webhook signing secret is required"))
	}
	return nil
}
func (d *DingTalk) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelDingTalk, "render", err)
	}
	text, degraded := truncateRunes(chatText(event), 3900)
	payload, err := marshalBounded(ChannelDingTalk, event.ID, endpoint.MaxPayloadBytes,
		map[string]any{"msgtype": "text", "text": map[string]string{"content": text}, "at": map[string]any{"isAtAll": false}})
	payload.Degraded = degraded
	return payload, err
}
func (d *DingTalk) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	decorate := func(request *http.Request, _ []byte, attemptTime time.Time) error {
		timestamp := strconv.FormatInt(attemptTime.UnixMilli(), 10)
		mac := hmac.New(sha256.New, delivery.Endpoint.Secret)
		_, _ = mac.Write([]byte(timestamp + "\n" + string(delivery.Endpoint.Secret)))
		signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		query := request.URL.Query()
		query.Set("timestamp", timestamp)
		query.Set("sign", signature)
		request.URL.RawQuery = query.Encode()
		return nil
	}
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, decorate)
	if err != nil {
		return Receipt{}, err
	}
	var response struct {
		ErrorCode any    `json:"errcode"`
		ErrorMsg  string `json:"errmsg"`
	}
	if json.Unmarshal([]byte(receipt.ResponseBody), &response) != nil {
		return Receipt{}, permanent(ChannelDingTalk, "provider response", errors.New("invalid JSON response"))
	}
	code := scalarProviderCode(response.ErrorCode)
	if code != "" && code != "0" {
		return Receipt{}, businessError(ChannelDingTalk, code, response.ErrorMsg,
			map[string]bool{"-1": true, "130101": true}, map[string]bool{"310000": true, "40035": true})
	}
	return receipt, nil
}
func (*DingTalk) Classify(err error) RetryDecision { return ClassifyError(err) }

type WeCom struct {
	sender HTTPDoer
	now    clock
}

func NewWeCom(sender HTTPDoer, options ...Option) *WeCom {
	if sender == nil {
		sender = defaultHTTPDoer()
	}
	return &WeCom{sender: sender, now: optionsClock(options)}
}
func (d *WeCom) Validate(ctx context.Context, endpoint Endpoint) error {
	if ctx == nil {
		return permanent(ChannelWeCom, "validate endpoint", errors.New("context is nil"))
	}
	return validateEndpoint(endpoint, ChannelWeCom, false)
}
func (d *WeCom) Render(ctx context.Context, event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	if err := d.Validate(ctx, endpoint); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelWeCom, "render", err)
	}
	text, degraded := truncateRunes(chatText(event), 4000)
	payload, err := marshalBounded(ChannelWeCom, event.ID, endpoint.MaxPayloadBytes,
		map[string]any{"msgtype": "text", "text": map[string]any{"content": text, "mentioned_list": []string{}}})
	payload.Degraded = degraded
	return payload, err
}
func (d *WeCom) Send(ctx context.Context, delivery Delivery, payload Payload) (Receipt, error) {
	if err := d.Validate(ctx, delivery.Endpoint); err != nil {
		return Receipt{}, err
	}
	receipt, err := sendHTTP(ctx, d.sender, d.now, delivery, payload, nil)
	if err != nil {
		return Receipt{}, err
	}
	var response struct {
		ErrorCode any    `json:"errcode"`
		ErrorMsg  string `json:"errmsg"`
	}
	if json.Unmarshal([]byte(receipt.ResponseBody), &response) != nil {
		return Receipt{}, permanent(ChannelWeCom, "provider response", errors.New("invalid JSON response"))
	}
	code := scalarProviderCode(response.ErrorCode)
	if code != "" && code != "0" {
		return Receipt{}, businessError(ChannelWeCom, code, response.ErrorMsg,
			map[string]bool{"-1": true, "45009": true}, map[string]bool{"40014": true, "42001": true, "93000": true})
	}
	return receipt, nil
}
func (*WeCom) Classify(err error) RetryDecision { return ClassifyError(err) }

func scalarProviderCode(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func firstResponse(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "provider rejected request"
}
