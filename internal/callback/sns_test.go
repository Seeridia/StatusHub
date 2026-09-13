package callback

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/transport"
)

type certificateTransport struct {
	body  []byte
	calls int
}

func (f *certificateTransport) Get(context.Context, string, transport.Conditional) (*transport.Response, error) {
	f.calls++
	return &transport.Response{StatusCode: 200, Body: f.body}, nil
}

func TestSNSVerifierAcceptsValidVersion2Signature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.us-east-1.amazonaws.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	envelope := SNSEnvelope{Type: "Notification", MessageID: "message-1", TopicARN: "arn:aws:sns:us-east-1:123456789012:ses",
		Message: `{"notificationType":"Delivery"}`, Timestamp: "2026-09-10T00:00:00Z", SignatureVersion: "2",
		SigningCertURL: "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem"}
	digest := sha256.Sum256([]byte(snsCanonicalString(envelope)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature = base64.StdEncoding.EncodeToString(signature)
	body, _ := json.Marshal(envelope)
	client := &certificateTransport{body: certificate}
	verifier, _ := NewSNSVerifier(client)
	verified, err := verifier.Verify(context.Background(), body)
	if err != nil || verified.MessageID != envelope.MessageID {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	if _, err := verifier.Verify(context.Background(), body); err != nil || client.calls != 1 {
		t.Fatalf("certificate cache calls=%d err=%v", client.calls, err)
	}
}

func TestSNSVerifierAcceptsAndConfirmsSubscription(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "sns.us-east-1.amazonaws.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	topic := "arn:aws:sns:us-east-1:123456789012:health"
	token := "opaque-token"
	envelope := SNSEnvelope{Type: "SubscriptionConfirmation", MessageID: "message-2", TopicARN: topic,
		Message: "You have chosen to subscribe", Timestamp: "2026-09-10T00:00:00Z", Token: token,
		SubscribeURL:     "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&TopicArn=" + url.QueryEscape(topic) + "&Token=" + token,
		SignatureVersion: "2", SigningCertURL: "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem"}
	digest := sha256.Sum256([]byte(snsCanonicalString(envelope)))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature = base64.StdEncoding.EncodeToString(signature)
	body, _ := json.Marshal(envelope)
	client := &certificateTransport{body: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
	verifier, _ := NewSNSVerifier(client)
	verified, err := verifier.Verify(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.ConfirmSubscription(context.Background(), verified); err != nil || client.calls != 2 {
		t.Fatalf("confirm calls=%d err=%v", client.calls, err)
	}
}
