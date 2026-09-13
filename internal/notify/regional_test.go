package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTeamsWorkflowContract(t *testing.T) {
	var sent map[string]any
	driver := NewTeams(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type=%q", request.Header.Get("Content-Type"))
		}
		_ = json.NewDecoder(request.Body).Decode(&sent)
		return response(http.StatusAccepted, nil, `1`), nil
	}))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelTeams, URL: "https://example.webhook.office.com/workflows/test"}
	receipt := renderAndSend(t, driver, endpoint)
	if receipt.HTTPStatus != http.StatusAccepted || sent["type"] != "message" {
		t.Fatalf("receipt=%#v payload=%#v", receipt, sent)
	}
}

func TestDiscordUsesWaitAndDisablesMentions(t *testing.T) {
	var requestURL *url.URL
	var sent struct {
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	driver := NewDiscord(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		requestURL = request.URL
		_ = json.NewDecoder(request.Body).Decode(&sent)
		return response(http.StatusOK, nil, `{"id":"discord-message"}`), nil
	}))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelDiscord, URL: "https://discord.com/api/webhooks/123/token"}
	receipt := renderAndSend(t, driver, endpoint)
	if requestURL.Query().Get("wait") != "true" || receipt.ProviderMessageID != "discord-message" || sent.AllowedMentions.Parse == nil {
		t.Fatalf("url=%s receipt=%#v payload=%#v", requestURL, receipt, sent)
	}
}

func TestTelegramParsesSuccessAndRetryAfter(t *testing.T) {
	token := "123456:abcdefghijklmnopqrstuvwxyz_ABCDE"
	t.Run("success", func(t *testing.T) {
		var path string
		driver := NewTelegram(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
			path = request.URL.Path
			return response(http.StatusOK, nil, `{"ok":true,"result":{"message_id":42}}`), nil
		}))
		endpoint := Endpoint{ID: "endpoint", Channel: ChannelTelegram, Secret: []byte(token), To: "-100123"}
		receipt := renderAndSend(t, driver, endpoint)
		if path != "/bot"+token+"/sendMessage" || receipt.ProviderMessageID != "42" {
			t.Fatalf("path=%q receipt=%#v", path, receipt)
		}
	})
	t.Run("rate limit", func(t *testing.T) {
		driver := NewTelegram(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusTooManyRequests, nil, `{"ok":false,"error_code":429,"parameters":{"retry_after":7}}`), nil
		}))
		endpoint := Endpoint{ID: "endpoint", Channel: ChannelTelegram, Secret: []byte(token), To: "1"}
		payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
		if err != nil {
			t.Fatal(err)
		}
		_, err = driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
		decision := driver.Classify(err)
		if decision.Class != ErrorClassRetryable || decision.RetryAfter != 7*time.Second {
			t.Fatalf("error=%v decision=%#v", err, decision)
		}
	})
}

func TestLarkSignsBodyAndClassifiesBusinessRateLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	var sent struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
	}
	responses := []string{`{"code":0,"msg":"success"}`, `{"code":9499,"msg":"rate limited"}`}
	driver := NewLark(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		_ = json.NewDecoder(request.Body).Decode(&sent)
		body := responses[0]
		responses = responses[1:]
		return response(http.StatusOK, nil, body), nil
	}), WithClock(func() time.Time { return now }))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelLark, URL: "https://open.feishu.cn/open-apis/bot/v2/hook/test", Secret: []byte("secret")}
	payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if err != nil || receipt.HTTPStatus != 200 || sent.Sign != larkSignature(sent.Timestamp, endpoint.Secret) {
		t.Fatalf("receipt=%#v error=%v sent=%#v", receipt, err, sent)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if decision := driver.Classify(err); decision.Class != ErrorClassRetryable {
		t.Fatalf("error=%v decision=%#v", err, decision)
	}
}

func TestDingTalkSignsAttemptURL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	var timestamp, signature string
	driver := NewDingTalk(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		timestamp, signature = request.URL.Query().Get("timestamp"), request.URL.Query().Get("sign")
		return response(http.StatusOK, nil, `{"errcode":0,"errmsg":"ok"}`), nil
	}), WithClock(func() time.Time { return now }))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelDingTalk, URL: "https://oapi.dingtalk.com/robot/send?access_token=test", Secret: []byte("secret")}
	renderAndSend(t, driver, endpoint)
	if timestamp != "1700000000000" || signature == "" {
		t.Fatalf("timestamp=%q sign=%q", timestamp, signature)
	}
}

func TestWeComBusinessAuthErrorDisablesEndpoint(t *testing.T) {
	driver := NewWeCom(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, nil, `{"errcode":40014,"errmsg":"invalid access token"}`), nil
	}))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelWeCom, URL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test"}
	payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, err = driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	decision := driver.Classify(err)
	if decision.Class != ErrorClassPermanent || !decision.DisableEndpoint {
		t.Fatalf("error=%v decision=%#v", err, decision)
	}
}

type fakeShoutrrrSender struct {
	url, message string
	err          error
}

func (s *fakeShoutrrrSender) Send(_ context.Context, serviceURL, message string) error {
	s.url, s.message = serviceURL, message
	return s.err
}

func TestShoutrrrBridgeKeepsOneAttemptBoundary(t *testing.T) {
	sender := &fakeShoutrrrSender{}
	driver := NewShoutrrr(sender)
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelShoutrrr, URL: "gotify://host/token"}
	receipt := renderAndSend(t, driver, endpoint)
	if receipt.Status != StatusProviderAccepted || sender.url != endpoint.URL || !strings.Contains(sender.message, "API outage") {
		t.Fatalf("receipt=%#v sender=%#v", receipt, sender)
	}
	sender.err = errors.New("temporary")
	payload, _ := driver.Render(context.Background(), channelEvent(), endpoint)
	_, err := driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if decision := driver.Classify(err); decision.Class != ErrorClassRetryable {
		t.Fatalf("error=%v decision=%#v", err, decision)
	}
}

func TestRegionalHTTPErrorClassificationMatrix(t *testing.T) {
	token := "123456:abcdefghijklmnopqrstuvwxyz_ABCDE"
	tests := []struct {
		name     string
		channel  Channel
		endpoint Endpoint
		new      func(HTTPDoer) ChannelDriver
	}{
		{"teams", ChannelTeams, Endpoint{URL: "https://example.webhook.office.com/workflows/test"}, func(doer HTTPDoer) ChannelDriver { return NewTeams(doer) }},
		{"discord", ChannelDiscord, Endpoint{URL: "https://discord.com/api/webhooks/123/token"}, func(doer HTTPDoer) ChannelDriver { return NewDiscord(doer) }},
		{"telegram", ChannelTelegram, Endpoint{Secret: []byte(token), To: "1"}, func(doer HTTPDoer) ChannelDriver { return NewTelegram(doer) }},
		{"lark", ChannelLark, Endpoint{URL: "https://open.feishu.cn/open-apis/bot/v2/hook/test", Secret: []byte("secret")}, func(doer HTTPDoer) ChannelDriver { return NewLark(doer) }},
		{"dingtalk", ChannelDingTalk, Endpoint{URL: "https://oapi.dingtalk.com/robot/send?access_token=test", Secret: []byte("secret")}, func(doer HTTPDoer) ChannelDriver { return NewDingTalk(doer) }},
		{"wecom", ChannelWeCom, Endpoint{URL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test"}, func(doer HTTPDoer) ChannelDriver { return NewWeCom(doer) }},
	}
	statuses := []struct {
		code    int
		class   ErrorClass
		disable bool
	}{
		{http.StatusTooManyRequests, ErrorClassRetryable, false},
		{http.StatusServiceUnavailable, ErrorClassRetryable, false},
		{http.StatusGone, ErrorClassPermanent, true},
		{http.StatusBadRequest, ErrorClassPermanent, false},
	}
	for _, test := range tests {
		for _, status := range statuses {
			t.Run(test.name+"/"+http.StatusText(status.code), func(t *testing.T) {
				driver := test.new(HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
					return response(status.code, nil, `{}`), nil
				}))
				endpoint := test.endpoint
				endpoint.ID, endpoint.Channel = "endpoint", test.channel
				payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
				if err != nil {
					t.Fatal(err)
				}
				_, err = driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
				decision := driver.Classify(err)
				if decision.Class != status.class || decision.DisableEndpoint != status.disable {
					t.Fatalf("HTTP %d error=%v decision=%#v", status.code, err, decision)
				}
			})
		}
	}
}

func renderAndSend(t *testing.T, driver ChannelDriver, endpoint Endpoint) Receipt {
	t.Helper()
	payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
