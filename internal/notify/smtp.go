package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Seeridia/StatusHub/internal/transport"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

//go:embed assets/statushub.png
var smtpLogo []byte

type SMTP struct{}

func NewSMTP() *SMTP { return &SMTP{} }
func (*SMTP) Validate(ctx context.Context, e Endpoint) error {
	fail := func() error {
		return permanent(ChannelSMTP, "validate", errors.New("invalid SMTP address, security mode, sender or recipient"))
	}
	if ctx == nil || e.Channel != ChannelSMTP || e.ID == "" {
		return fail()
	}
	host, port, err := net.SplitHostPort(e.SMTPAddress)
	if err != nil || strings.TrimSpace(host) == "" || (port != "465" && port != "587" && port != "2525") || (e.SMTPSecurity != "tls" && e.SMTPSecurity != "starttls") {
		return fail()
	}
	for _, a := range []string{e.From, e.To} {
		if strings.ContainsAny(a, "\r\n") {
			return fail()
		}
		if _, err := mail.ParseAddress(a); err != nil {
			return fail()
		}
	}
	if e.SMTPUsername != "" && len(e.Secret) == 0 {
		return fail()
	}
	return nil
}
func (d *SMTP) Render(ctx context.Context, event CanonicalEvent, e Endpoint) (Payload, error) {
	if err := d.Validate(ctx, e); err != nil {
		return Payload{}, err
	}
	if err := validateCanonicalEvent(event); err != nil {
		return Payload{}, permanent(ChannelSMTP, "render", err)
	}
	message, err := renderEmail(event)
	if err != nil {
		return Payload{}, permanent(ChannelSMTP, "render", errors.New("email template failed"))
	}
	return marshalBounded(ChannelSMTP, event.ID, e.MaxPayloadBytes, message)
}
func (*SMTP) Classify(err error) RetryDecision { return ClassifyError(err) }
func (d *SMTP) Send(ctx context.Context, delivery Delivery, p Payload) (Receipt, error) {
	e := delivery.Endpoint
	if err := d.Validate(ctx, e); err != nil {
		return Receipt{}, err
	}
	if p.Channel != ChannelSMTP || p.EventID != delivery.EventID {
		return Receipt{}, permanent(ChannelSMTP, "send", errors.New("invalid SMTP payload"))
	}
	var message emailMessage
	if json.Unmarshal(p.Body, &message) != nil {
		return Receipt{}, permanent(ChannelSMTP, "send", errors.New("invalid email message"))
	}
	fail := func(stage string, err error) (Receipt, error) {
		class := ErrorClassRetryable
		var pe *textproto.Error
		if errors.As(err, &pe) && pe.Code >= 500 {
			class = ErrorClassPermanent
		}
		return Receipt{}, &Error{Channel: ChannelSMTP, Operation: stage, Class: class, Err: errors.New("SMTP operation failed")}
	}
	conn, err := transport.DialPublic(ctx, e.SMTPAddress)
	if err != nil {
		return fail("connect", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(25 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetDeadline(deadline)
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	host, _, _ := net.SplitHostPort(e.SMTPAddress)
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if e.SMTPSecurity == "tls" {
		secure := tls.Client(conn, config)
		if err = secure.HandshakeContext(ctx); err != nil {
			return fail("TLS", err)
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fail("greeting", err)
	}
	defer client.Close()
	if e.SMTPSecurity == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return Receipt{}, permanent(ChannelSMTP, "TLS", errors.New("server does not support STARTTLS"))
		}
		if err = client.StartTLS(config); err != nil {
			return fail("TLS", err)
		}
	}
	if e.SMTPUsername != "" {
		if err = client.Auth(smtp.PlainAuth("", e.SMTPUsername, string(e.Secret), host)); err != nil {
			return fail("authenticate", err)
		}
	}
	from, _ := mail.ParseAddress(e.From)
	to, _ := mail.ParseAddress(e.To)
	if err = client.Mail(from.Address); err != nil {
		return fail("sender", err)
	}
	if err = client.Rcpt(to.Address); err != nil {
		return fail("recipient", err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct{ kind, text string }{{"text/plain", message.Text}, {"text/html", message.HTML}} {
		h := textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=UTF-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}
		w, err := mw.CreatePart(h)
		if err != nil {
			return fail("encode", err)
		}
		q := quotedprintable.NewWriter(w)
		if _, err = q.Write([]byte(part.text)); err != nil {
			return fail("encode", err)
		}
		if err = q.Close(); err != nil {
			return fail("encode", err)
		}
	}
	if err = mw.Close(); err != nil {
		return fail("encode", err)
	}
	// Embed the logo so clients do not need to fetch an external image.
	var related bytes.Buffer
	rw := multipart.NewWriter(&related)
	alternative, err := rw.CreatePart(textproto.MIMEHeader{"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%q", mw.Boundary())}})
	if err != nil {
		return fail("encode", err)
	}
	if _, err = alternative.Write(body.Bytes()); err != nil {
		return fail("encode", err)
	}
	logo, err := rw.CreatePart(textproto.MIMEHeader{"Content-Type": {"image/png"}, "Content-ID": {"<statushub-logo>"}, "Content-Disposition": {"inline; filename=statushub.png"}, "Content-Transfer-Encoding": {"base64"}})
	if err != nil {
		return fail("encode", err)
	}
	encoded := base64.StdEncoding.EncodeToString(smtpLogo)
	for len(encoded) > 0 {
		n := min(76, len(encoded))
		if _, err = fmt.Fprint(logo, encoded[:n]+"\r\n"); err != nil {
			return fail("encode", err)
		}
		encoded = encoded[n:]
	}
	if err = rw.Close(); err != nil {
		return fail("encode", err)
	}
	header := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=%q\r\n\r\n", from.String(), to.String(), mime.QEncoding.Encode("UTF-8", strings.Join(strings.Fields(message.Subject), " ")), time.Now().UTC().Format(time.RFC1123Z), rw.Boundary())
	w, err := client.Data()
	if err != nil {
		return fail("data", err)
	}
	if _, err = w.Write(append([]byte(header), related.Bytes()...)); err != nil {
		return fail("write", err)
	}
	if err = w.Close(); err != nil {
		return fail("accept", err)
	}
	// DATA success is authoritative; a failed QUIT must not resend an accepted email.
	_ = client.Quit()
	return Receipt{Status: StatusProviderAccepted, AcceptedAt: time.Now().UTC(), Degraded: p.Degraded}, nil
}
