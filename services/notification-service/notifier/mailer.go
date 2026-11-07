// Package notifier turns domain events into customer emails.
package notifier

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/Varsh1l01/VENDORA/pkg/logx"
	"github.com/Varsh1l01/VENDORA/pkg/validate"
)

// Message is an email to send.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends messages.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// LogMailer logs messages instead of sending them (development). Recipient
// addresses are redacted and bodies are not logged.
type LogMailer struct{}

// Send implements Mailer.
func (LogMailer) Send(ctx context.Context, m Message) error {
	logx.From(ctx).Info("email (log mode)", "to", logx.RedactEmail(m.To), "subject", m.Subject, "body_bytes", len(m.Body))
	return nil
}

// SMTPMailer sends through an SMTP server with STARTTLS when offered.
type SMTPMailer struct {
	Host, Port, Username, Password, From string
}

// Send implements Mailer. Header values are validated so user-controlled
// data cannot inject headers.
func (s SMTPMailer) Send(ctx context.Context, m Message) error {
	to, err := validate.Email(m.To)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return fmt.Errorf("invalid subject")
	}
	msg := strings.Join([]string{
		"From: " + s.From,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"",
		m.Body,
	}, "\r\n")

	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	errc := make(chan error, 1)
	go func() {
		errc <- smtp.SendMail(net.JoinHostPort(s.Host, s.Port), auth, s.From, []string{to}, []byte(msg))
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
