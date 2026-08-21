package email

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html,omitempty"`
}

type Provider interface {
	Send(context.Context, Message) error
}

type CaptureProvider struct{ Directory string }

func (p *CaptureProvider) Send(_ context.Context, message Message) error {
	if strings.TrimSpace(p.Directory) == "" {
		return errors.New("mail capture directory is required")
	}
	if err := os.MkdirAll(p.Directory, 0o750); err != nil {
		return fmt.Errorf("create mail capture directory: %w", err)
	}
	data, err := json.MarshalIndent(message, "", "  ")
	if err != nil {
		return err
	}
	name := message.ID
	if name == "" || strings.ContainsAny(name, "/\\") {
		return errors.New("mail capture message ID is invalid")
	}
	root, err := os.OpenRoot(p.Directory)
	if err != nil {
		return fmt.Errorf("open mail capture directory: %w", err)
	}
	defer root.Close()
	file, err := root.OpenFile(name+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create mail capture: %w", err)
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write mail capture: %w", err)
	}
	return file.Close()
}

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLSMode  string
	Timeout  time.Duration
}

type SMTPProvider struct{ config SMTPConfig }

func NewSMTPProvider(config SMTPConfig) (*SMTPProvider, error) {
	config.Host = strings.TrimSpace(config.Host)
	if config.Host == "" {
		return nil, errors.New("SMTP host is required")
	}
	if config.Port <= 0 || config.Port > 65535 {
		return nil, errors.New("SMTP port is invalid")
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	if config.TLSMode != "tls" && config.TLSMode != "starttls" && config.TLSMode != "none" {
		return nil, errors.New("SMTP TLS mode is invalid")
	}
	return &SMTPProvider{config: config}, nil
}

func (p *SMTPProvider) Send(ctx context.Context, message Message) error {
	from, err := mail.ParseAddress(message.From)
	if err != nil {
		return errors.New("mail From address is invalid")
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return errors.New("mail recipient address is invalid")
	}
	if headerUnsafe(message.Subject) {
		return errors.New("mail subject contains invalid characters")
	}
	addr := net.JoinHostPort(p.config.Host, strconv.Itoa(p.config.Port))
	dialer := &net.Dialer{Timeout: p.config.Timeout}
	var connection net.Conn
	if p.config.TLSMode == "tls" {
		connection, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.config.Host})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(p.config.Timeout))
	client, err := smtp.NewClient(connection, p.config.Host)
	if err != nil {
		return fmt.Errorf("initialize SMTP: %w", err)
	}
	defer client.Close()
	if p.config.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not advertise STARTTLS")
		}
		if err = client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.config.Host}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if p.config.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", p.config.Username, p.config.Password, p.config.Host)); err != nil {
			return fmt.Errorf("authenticate SMTP: %w", err)
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err = client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP data: %w", err)
	}
	if _, err = writer.Write(renderMIME(message, from, to)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP data: %w", err)
	}
	if err = writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP data: %w", err)
	}
	return client.Quit()
}

func renderMIME(message Message, from, to *mail.Address) []byte {
	boundary := "modeldock-alternative-" + message.ID
	var body strings.Builder
	body.WriteString("From: " + from.String() + "\r\n")
	body.WriteString("To: " + to.String() + "\r\n")
	body.WriteString("Subject: " + message.Subject + "\r\n")
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	body.WriteString("--" + boundary + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + message.Text + "\r\n")
	if message.HTML != "" {
		body.WriteString("--" + boundary + "\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" + message.HTML + "\r\n")
	}
	body.WriteString("--" + boundary + "--\r\n")
	return []byte(body.String())
}

func headerUnsafe(value string) bool { return strings.ContainsAny(value, "\r\n") }
