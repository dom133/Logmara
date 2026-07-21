package notify

import (
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
)

type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	UseTLS   bool
}

type EmailNotifier struct {
	SMTP SMTPConfig
	To   []string
}

func (n *EmailNotifier) Send(payload Payload) error {
	if len(n.To) == 0 {
		return fmt.Errorf("email channel has no recipients configured")
	}
	if n.SMTP.Host == "" {
		return fmt.Errorf("SMTP is not configured (set it up under Admin > Settings)")
	}

	addr := fmt.Sprintf("%s:%s", n.SMTP.Host, n.SMTP.Port)
	subject := fmt.Sprintf("[Syslytics] %s", payload.Title)
	body := payload.Message
	if payload.Link != "" {
		body += "\n\n" + payload.Link
	}
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		n.SMTP.From, strings.Join(n.To, ", "), subject, body))

	var auth smtp.Auth
	if n.SMTP.Username != "" {
		auth = smtp.PlainAuth("", n.SMTP.Username, n.SMTP.Password, n.SMTP.Host)
	}

	if n.SMTP.UseTLS {
		return sendWithSTARTTLS(addr, n.SMTP.Host, auth, n.SMTP.From, n.To, msg)
	}
	return smtp.SendMail(addr, auth, n.SMTP.From, n.To, msg)
}

// sendWithSTARTTLS mirrors smtp.SendMail but upgrades the connection with
// STARTTLS when the server advertises it, since smtp.SendMail itself only
// does plaintext or implicit-TLS-on-connect, not STARTTLS.
func sendWithSTARTTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial smtp server: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write smtp body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close smtp writer: %w", err)
	}
	return c.Quit()
}
