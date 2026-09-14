package controlplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// SMTP is dedicated to identity mail, separate from incident delivery channels.
func (s *Server) RunIdentityMail(ctx context.Context) error {
	repo, ok := s.repository.(interface {
		RunIdentityMail(context.Context, func(context.Context, string) error) error
	})
	if !ok {
		return nil
	}
	return repo.RunIdentityMail(ctx, func(ctx context.Context, sealed string) error {
		address := os.Getenv("STATUSHUB_SMTP_ADDRESS")
		if address == "" {
			return errors.New("identity SMTP is not configured")
		}
		body, e := (teamCipher{s.sessions.codec}).Decode(sealed)
		if e != nil {
			return errors.New("identity email cannot be decrypted")
		}
		var data struct {
			To      string `json:"to"`
			Purpose string `json:"purpose"`
			URL     string `json:"url"`
		}
		if e = json.Unmarshal(body, &data); e != nil {
			return e
		}
		from := os.Getenv("STATUSHUB_SMTP_FROM")
		if from == "" {
			from = "statushub@localhost"
		}
		if _, e = mail.ParseAddress(from); e != nil || strings.ContainsAny(from, "\r\n") {
			return errors.New("invalid mail sender")
		}
		if _, e = mail.ParseAddress(data.To); e != nil || strings.ContainsAny(data.To, "\r\n") {
			return errors.New("invalid mail recipient")
		}
		host, _, e := net.SplitHostPort(address)
		if e != nil {
			return errors.New("invalid SMTP address")
		}
		conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
		if e != nil {
			return errors.New("identity SMTP connection failed")
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(20 * time.Second))
		client, e := smtp.NewClient(conn, host)
		if e != nil {
			return errors.New("identity SMTP handshake failed")
		}
		defer client.Close()
		if supported, _ := client.Extension("STARTTLS"); supported {
			if e = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); e != nil {
				return errors.New("identity SMTP TLS failed")
			}
		} else {
			ip := net.ParseIP(host)
			if os.Getenv("STATUSHUB_SMTP_ALLOW_LOCAL_PLAINTEXT") != "true" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
				return errors.New("identity SMTP requires TLS")
			}
		}
		if username := os.Getenv("STATUSHUB_SMTP_USERNAME"); username != "" {
			if e = client.Auth(smtp.PlainAuth("", username, os.Getenv("STATUSHUB_SMTP_PASSWORD"), host)); e != nil {
				return errors.New("identity SMTP authentication failed")
			}
		}
		if e = client.Mail(from); e != nil {
			return errors.New("identity SMTP sender rejected")
		}
		if e = client.Rcpt(data.To); e != nil {
			return errors.New("identity SMTP recipient rejected")
		}
		writer, e := client.Data()
		if e != nil {
			return errors.New("identity SMTP data failed")
		}
		subject := "Verify your StatusHub email / 验证邮箱"
		if data.Purpose == "invite" {
			subject = "Join your StatusHub workspace / 加入工作区"
		}
		if data.Purpose == "reset" {
			subject = "Reset your StatusHub password / 重置密码"
		}
		message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nOpen the link to continue. If you did not request this, ignore this email.\r\n请打开链接继续。如果不是您本人发起，请忽略。\r\n\r\n%s\r\n", from, data.To, mime.QEncoding.Encode("UTF-8", subject), data.URL)
		if _, e = writer.Write([]byte(message)); e != nil {
			return errors.New("identity SMTP write failed")
		}
		if e = writer.Close(); e != nil {
			return errors.New("identity SMTP delivery failed")
		}
		return client.Quit()
	})
}
