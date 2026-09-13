package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type fakeSES struct{ message SESMessage }

func (f *fakeSES) SendEmail(_ context.Context, message SESMessage) (string, error) {
	f.message = message
	return "ses-message", nil
}

func channelEvent() CanonicalEvent {
	return CanonicalEvent{ID: domain.CanonicalEventID("event-1"), Source: "https://status.example.test", Kind: domain.EventKindIncidentCreated,
		Subject: "API outage", Time: time.Unix(1, 0), Revision: 1, SchemaVersion: "v1", Summary: "API unavailable", Data: json.RawMessage(`{"current":{"impact":"critical"}}`)}
}

func TestPagerDutyUsesStableDedupKey(t *testing.T) {
	var sent []byte
	driver := NewPagerDuty(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		sent, _ = io.ReadAll(request.Body)
		return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(`{"status":"success","dedup_key":"event-1"}`)), Header: make(http.Header)}, nil
	}))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelPagerDuty, Secret: []byte("routing-key")}
	payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ProviderMessageID != "event-1" || !strings.Contains(string(sent), `"dedup_key":"event-1"`) {
		t.Fatalf("receipt=%#v body=%s", receipt, sent)
	}
}

func TestSESAcceptanceCarriesMessageID(t *testing.T) {
	client := &fakeSES{}
	driver := NewSES(client)
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelEmailSES, From: "status@example.test", To: "oncall@example.test"}
	payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ProviderMessageID != "ses-message" || client.message.DeliveryID != "delivery" {
		t.Fatalf("receipt=%#v message=%#v", receipt, client.message)
	}
}

func TestTwilioRequestHasBasicAuthAndStatusCallback(t *testing.T) {
	var body string
	driver := NewTwilioSMS(HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		user, password, ok := request.BasicAuth()
		if !ok || user != "AC123" || password != "token" {
			t.Fatalf("auth=%q %q %v", user, password, ok)
		}
		data, _ := io.ReadAll(request.Body)
		body = string(data)
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"sid":"SM123"}`)), Header: make(http.Header)}, nil
	}))
	endpoint := Endpoint{ID: "endpoint", Channel: ChannelTwilioSMS, URL: "https://api.twilio.com/2010-04-01/Accounts/AC123/Messages.json", AccountSID: "AC123", Secret: []byte("token"), From: "+100", To: "+200", CallbackURL: "https://callbacks.example.test/twilio"}
	payload, err := driver.Render(context.Background(), channelEvent(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := driver.Send(context.Background(), Delivery{ID: "delivery", EventID: "event-1", Endpoint: endpoint}, payload)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ProviderMessageID != "SM123" || !strings.Contains(body, "StatusCallback=") {
		t.Fatalf("receipt=%#v body=%s", receipt, body)
	}
}
