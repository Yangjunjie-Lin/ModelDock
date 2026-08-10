package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 32 << 10

type Config struct {
	Timeout             time.Duration
	AllowHTTP           bool
	AllowPrivateNetwork bool
}

type Delivery struct {
	ID        string
	EventType string
	URL       string
	Secret    string
	Body      []byte
}

type Result struct {
	HTTPStatus int
	Response   string
}

type Dispatcher struct {
	client              *http.Client
	allowHTTP           bool
	allowPrivateNetwork bool
	now                 func() time.Time
}

func New(cfg Config) *Dispatcher {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dispatcher := &Dispatcher{
		allowHTTP:           cfg.AllowHTTP,
		allowPrivateNetwork: cfg.AllowPrivateNetwork,
		now:                 time.Now,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Webhook delivery never inherits process proxy settings. The custom dialer
	// validates the exact address it connects to, closing the DNS-rebinding gap
	// between an initial URL check and the network connection.
	transport.Proxy = nil
	transport.DialContext = dispatcher.dialContext
	dispatcher.client = &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("webhook redirects are disabled")
		},
	}
	return dispatcher
}

func (d *Dispatcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid webhook network address")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("webhook hostname could not be resolved")
	}
	dialer := net.Dialer{Timeout: d.client.Timeout}
	var lastErr error
	for _, ip := range ips {
		if !d.allowPrivateNetwork && unsafeWebhookIP(ip) {
			lastErr = errors.New("webhook URL resolves to a private or local address")
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("webhook target has no allowed address")
	}
	return nil, lastErr
}

func (d *Dispatcher) ValidateTarget(ctx context.Context, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" || u.User != nil {
		return errors.New("webhook URL must be an absolute URL without user information")
	}
	if u.Scheme != "https" && !(d.allowHTTP && u.Scheme == "http") {
		return errors.New("webhook URL must use HTTPS")
	}
	if d.allowPrivateNetwork {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return errors.New("webhook hostname could not be resolved")
	}
	for _, ip := range ips {
		if unsafeWebhookIP(ip) {
			return errors.New("webhook URL resolves to a private or local address")
		}
	}
	return nil
}

func unsafeWebhookIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func (d *Dispatcher) Deliver(ctx context.Context, delivery Delivery) (Result, error) {
	if err := d.ValidateTarget(ctx, delivery.URL); err != nil {
		return Result{}, err
	}
	if delivery.ID == "" || delivery.EventType == "" || len(delivery.Secret) < 16 {
		return Result{}, errors.New("delivery ID, event type, and a signing secret of at least 16 characters are required")
	}
	timestamp := strconv.FormatInt(d.now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(delivery.Secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(delivery.Body)
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, bytes.NewReader(delivery.Body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RelayDock-Webhook/2.0")
	req.Header.Set("X-RelayDock-Event", delivery.EventType)
	req.Header.Set("X-RelayDock-Delivery", delivery.ID)
	req.Header.Set("X-RelayDock-Timestamp", timestamp)
	req.Header.Set("X-RelayDock-Signature", signature)

	resp, err := d.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		return Result{HTTPStatus: resp.StatusCode}, readErr
	}
	if len(body) > maxResponseBytes {
		body = body[:maxResponseBytes]
	}
	result := Result{HTTPStatus: resp.StatusCode, Response: string(body)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("webhook endpoint returned HTTP %d", resp.StatusCode)
	}
	return result, nil
}
