// Package callback verifies and normalizes asynchronous provider delivery
// receipts before they enter the durable delivery ledger.
package callback

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- mandated by Twilio's webhook signature protocol.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type Event struct {
	ProviderEventID   string
	ProviderMessageID string
	DeliveryID        *string
	Status            string
	Payload           json.RawMessage
}

func DecodeSES(body []byte) (Event, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return Event{}, errors.New("callback: invalid SES JSON")
	}
	payload := body
	var snsMessage string
	var snsMessageID string
	if raw := root["MessageId"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &snsMessageID)
	}
	if raw := root["Message"]; len(raw) > 0 && json.Unmarshal(raw, &snsMessage) == nil && json.Valid([]byte(snsMessage)) {
		payload = []byte(snsMessage)
		_ = json.Unmarshal(payload, &root)
	}
	type mail struct {
		MessageID string              `json:"messageId"`
		Tags      map[string][]string `json:"tags"`
	}
	var direct struct {
		ID               string `json:"id"`
		DetailType       string `json:"detail-type"`
		NotificationType string `json:"notificationType"`
		Mail             mail   `json:"mail"`
		Detail           struct {
			Mail             mail   `json:"mail"`
			EventType        string `json:"eventType"`
			NotificationType string `json:"notificationType"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(payload, &direct); err != nil {
		return Event{}, errors.New("callback: invalid SES event")
	}
	eventType := direct.NotificationType
	message := direct.Mail
	if direct.Detail.Mail.MessageID != "" {
		message = direct.Detail.Mail
	}
	if eventType == "" {
		eventType = direct.Detail.EventType
	}
	if eventType == "" {
		eventType = direct.DetailType
	}
	status := sesStatus(eventType)
	if message.MessageID == "" || status == "" {
		return Event{}, errors.New("callback: SES message ID or event type is missing")
	}
	var deliveryID *string
	if values := message.Tags["delivery_id"]; len(values) > 0 && values[0] != "" {
		value := values[0]
		deliveryID = &value
	}
	eventID := direct.ID
	if eventID == "" {
		eventID = snsMessageID
	}
	if eventID == "" {
		eventID = message.MessageID + ":" + strings.ToLower(eventType)
	}
	return Event{ProviderEventID: eventID, ProviderMessageID: message.MessageID,
		DeliveryID: deliveryID, Status: status, Payload: append([]byte(nil), payload...)}, nil
}

func DecodeTwilio(form url.Values) (Event, error) {
	messageID := strings.TrimSpace(form.Get("MessageSid"))
	rawStatus := strings.ToLower(strings.TrimSpace(form.Get("MessageStatus")))
	if messageID == "" || rawStatus == "" {
		return Event{}, errors.New("callback: Twilio MessageSid and MessageStatus are required")
	}
	status := "accepted"
	switch rawStatus {
	case "delivered", "read":
		status = "delivered"
	case "failed", "undelivered":
		status = "failed"
	case "canceled":
		status = "suppressed"
	}
	payload, _ := json.Marshal(form)
	return Event{ProviderEventID: messageID + ":" + rawStatus, ProviderMessageID: messageID,
		Status: status, Payload: payload}, nil
}

func VerifyTwilioSignature(authToken, requestURL, signature string, form url.Values) bool {
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	value := requestURL
	for _, key := range keys {
		values := append([]string(nil), form[key]...)
		sort.Strings(values)
		for _, item := range values {
			value += key + item
		}
	}
	mac := hmac.New(sha1.New, []byte(authToken)) // #nosec G401 -- protocol compatibility.
	_, _ = mac.Write([]byte(value))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func ToStore(endpointID string, event Event, body []byte, receivedAt time.Time) store.ProviderCallback {
	hash := sha256.Sum256(body)
	return store.ProviderCallback{EndpointID: endpointID, DeliveryID: event.DeliveryID,
		ProviderEventID: event.ProviderEventID, ProviderMessageID: event.ProviderMessageID,
		ReceivedAt: receivedAt.UTC(), RawBodySHA256: hash[:], Payload: event.Payload, DeliveryStatus: event.Status}
}

func sesStatus(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "delivery"):
		return "delivered"
	case strings.Contains(value, "bounce"), strings.Contains(value, "reject"), strings.Contains(value, "rendering failure"):
		return "failed"
	case strings.Contains(value, "complaint"):
		return "suppressed"
	default:
		return ""
	}
}

func HashHex(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
