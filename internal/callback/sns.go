package callback

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- SNS SignatureVersion 1 requires SHA-1.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Seeridia/StatusHub/internal/transport"
)

type SNSEnvelope struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn"`
	Subject          string `json:"Subject"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	SubscribeURL     string `json:"SubscribeURL"`
	Token            string `json:"Token"`
}

type cachedCertificate struct {
	certificate *x509.Certificate
	expiresAt   time.Time
}

type SNSVerifier struct {
	client       transport.Client
	mu           sync.Mutex
	certificates map[string]cachedCertificate
	now          func() time.Time
}

func NewSNSVerifier(client transport.Client) (*SNSVerifier, error) {
	if client == nil {
		return nil, errors.New("callback: SNS verifier transport is required")
	}
	return &SNSVerifier{client: client, certificates: make(map[string]cachedCertificate), now: time.Now}, nil
}

func (v *SNSVerifier) Verify(ctx context.Context, body []byte) (SNSEnvelope, error) {
	var envelope SNSEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return SNSEnvelope{}, errors.New("callback: invalid SNS envelope")
	}
	if envelope.MessageID == "" || envelope.TopicARN == "" || envelope.Message == "" ||
		envelope.Timestamp == "" || envelope.Signature == "" || envelope.SigningCertURL == "" {
		return SNSEnvelope{}, errors.New("callback: incomplete SNS notification")
	}
	switch envelope.Type {
	case "Notification":
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		if envelope.SubscribeURL == "" || envelope.Token == "" {
			return SNSEnvelope{}, errors.New("callback: incomplete SNS confirmation")
		}
	default:
		return SNSEnvelope{}, errors.New("callback: unsupported SNS message type")
	}
	if err := validateSNSCertificateURL(envelope.SigningCertURL); err != nil {
		return SNSEnvelope{}, err
	}
	certificate, err := v.certificate(ctx, envelope.SigningCertURL)
	if err != nil {
		return SNSEnvelope{}, err
	}
	now := v.now()
	if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return SNSEnvelope{}, errors.New("callback: SNS signing certificate is expired or not yet valid")
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return SNSEnvelope{}, errors.New("callback: SNS certificate is not RSA")
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		return SNSEnvelope{}, errors.New("callback: invalid SNS signature encoding")
	}
	canonical := []byte(snsCanonicalString(envelope))
	var digest []byte
	var algorithm crypto.Hash
	switch envelope.SignatureVersion {
	case "1":
		sum := sha1.Sum(canonical) // #nosec G401 -- protocol compatibility.
		digest, algorithm = sum[:], crypto.SHA1
	case "2":
		sum := sha256.Sum256(canonical)
		digest, algorithm = sum[:], crypto.SHA256
	default:
		return SNSEnvelope{}, errors.New("callback: unsupported SNS signature version")
	}
	if err := rsa.VerifyPKCS1v15(publicKey, algorithm, digest, signature); err != nil {
		return SNSEnvelope{}, errors.New("callback: SNS signature verification failed")
	}
	return envelope, nil
}

func (v *SNSVerifier) certificate(ctx context.Context, certificateURL string) (*x509.Certificate, error) {
	now := v.now()
	v.mu.Lock()
	cached, found := v.certificates[certificateURL]
	v.mu.Unlock()
	if found && now.Before(cached.expiresAt) {
		return cached.certificate, nil
	}
	response, err := v.client.Get(ctx, certificateURL, transport.Conditional{})
	if err != nil || response == nil || response.StatusCode != 200 {
		return nil, fmt.Errorf("callback: fetch SNS signing certificate failed")
	}
	block, _ := pem.Decode(response.Body)
	if block == nil {
		return nil, errors.New("callback: invalid SNS certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("callback: invalid SNS certificate")
	}
	expiresAt := certificate.NotAfter
	if maximum := now.Add(24 * time.Hour); maximum.Before(expiresAt) {
		expiresAt = maximum
	}
	v.mu.Lock()
	v.certificates[certificateURL] = cachedCertificate{certificate: certificate, expiresAt: expiresAt}
	v.mu.Unlock()
	return certificate, nil
}

func (v *SNSVerifier) ConfirmSubscription(ctx context.Context, envelope SNSEnvelope) error {
	if envelope.Type != "SubscriptionConfirmation" {
		return errors.New("callback: SNS message is not a subscription confirmation")
	}
	if err := validateSNSConfirmationURL(envelope.SubscribeURL, envelope.TopicARN, envelope.Token); err != nil {
		return err
	}
	response, err := v.client.Get(ctx, envelope.SubscribeURL, transport.Conditional{})
	if err != nil || response == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("callback: confirm SNS subscription failed")
	}
	return nil
}

func validateSNSCertificateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("callback: invalid SNS certificate URL")
	}
	host := strings.ToLower(parsed.Hostname())
	validHost := (strings.HasPrefix(host, "sns.") && strings.HasSuffix(host, ".amazonaws.com")) ||
		(strings.HasPrefix(host, "sns.") && strings.HasSuffix(host, ".amazonaws.com.cn"))
	if !validHost || !strings.HasSuffix(strings.ToLower(parsed.Path), ".pem") {
		return errors.New("callback: untrusted SNS certificate URL")
	}
	return nil
}

func snsCanonicalString(value SNSEnvelope) string {
	var builder strings.Builder
	fields := []struct{ name, value string }{{"Message", value.Message}, {"MessageId", value.MessageID}}
	if value.Type == "Notification" {
		fields = append(fields, struct{ name, value string }{"Subject", value.Subject})
	} else {
		fields = append(fields,
			struct{ name, value string }{"SubscribeURL", value.SubscribeURL},
			struct{ name, value string }{"Timestamp", value.Timestamp},
			struct{ name, value string }{"Token", value.Token},
			struct{ name, value string }{"TopicArn", value.TopicARN},
			struct{ name, value string }{"Type", value.Type},
		)
	}
	if value.Type == "Notification" {
		fields = append(fields,
			struct{ name, value string }{"Timestamp", value.Timestamp},
			struct{ name, value string }{"TopicArn", value.TopicARN},
			struct{ name, value string }{"Type", value.Type},
		)
	}
	for _, field := range fields {
		if field.value != "" {
			builder.WriteString(field.name)
			builder.WriteByte('\n')
			builder.WriteString(field.value)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func validateSNSConfirmationURL(raw, topicARN, token string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("callback: invalid SNS confirmation URL")
	}
	parts := strings.Split(topicARN, ":")
	if len(parts) < 6 || parts[0] != "arn" || parts[2] != "sns" || parts[3] == "" {
		return errors.New("callback: invalid SNS topic ARN")
	}
	suffix := ".amazonaws.com"
	if parts[1] == "aws-cn" {
		suffix = ".amazonaws.com.cn"
	}
	if !strings.EqualFold(parsed.Hostname(), "sns."+parts[3]+suffix) || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("callback: SNS confirmation host does not match topic")
	}
	query := parsed.Query()
	if query.Get("Action") != "ConfirmSubscription" || query.Get("TopicArn") != topicARN || query.Get("Token") != token {
		return errors.New("callback: SNS confirmation parameters do not match")
	}
	return nil
}
