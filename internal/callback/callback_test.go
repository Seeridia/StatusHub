package callback

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- Twilio protocol fixture.
	"encoding/base64"
	"net/url"
	"testing"
)

func TestDecodeSESFromSNS(t *testing.T) {
	body := []byte(`{"Type":"Notification","MessageId":"sns-1","Message":"{\"notificationType\":\"Delivery\",\"mail\":{\"messageId\":\"ses-1\",\"tags\":{\"delivery_id\":[\"delivery-1\"]}}}"}`)
	event, err := DecodeSES(body)
	if err != nil || event.ProviderEventID != "sns-1" || event.ProviderMessageID != "ses-1" ||
		event.DeliveryID == nil || *event.DeliveryID != "delivery-1" || event.Status != "delivered" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestTwilioSignatureAndStatus(t *testing.T) {
	values := url.Values{"MessageSid": {"SM123"}, "MessageStatus": {"delivered"}}
	callbackURL := "https://example.test/v1/provider-callbacks/twilio/endpoint"
	canonical := callbackURL + "MessageSidSM123MessageStatusdelivered"
	mac := hmac.New(sha1.New, []byte("token")) // #nosec G401 -- Twilio protocol fixture.
	_, _ = mac.Write([]byte(canonical))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !VerifyTwilioSignature("token", callbackURL, signature, values) {
		t.Fatal("signature did not verify")
	}
	event, err := DecodeTwilio(values)
	if err != nil || event.Status != "delivered" || event.ProviderEventID != "SM123:delivered" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
}

func TestSNSCertificateURLPolicy(t *testing.T) {
	for _, raw := range []string{
		"http://sns.us-east-1.amazonaws.com/cert.pem",
		"https://sns.us-east-1.amazonaws.com.evil.test/cert.pem",
		"https://169.254.169.254/cert.pem",
	} {
		if validateSNSCertificateURL(raw) == nil {
			t.Fatalf("accepted unsafe URL %q", raw)
		}
	}
	if err := validateSNSCertificateURL("https://sns.us-east-1.amazonaws.com/SimpleNotificationService.pem"); err != nil {
		t.Fatal(err)
	}
}
