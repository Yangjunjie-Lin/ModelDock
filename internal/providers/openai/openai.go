package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/relayedock/relayedock/internal/providers"
)

type Adapter struct {
	client *http.Client
	policy *EndpointPolicy
}

type EndpointPolicy struct {
	AllowedHosts        []string
	AllowPrivateNetwork bool
	AllowHTTP           bool
}

type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("upstream provider API returned HTTP %d", e.StatusCode)
}

func New(client *http.Client) *Adapter {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 200, MaxIdleConnsPerHost: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second, ExpectContinueTimeout: 1 * time.Second}}
	}
	return &Adapter{client: client}
}

func NewWithPolicy(client *http.Client, policy EndpointPolicy) *Adapter {
	if client == nil {
		transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 200, MaxIdleConnsPerHost: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second, ExpectContinueTimeout: time.Second}
		transport.DialContext = policy.dialContext
		client = &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("provider redirects are disabled")
		}}
	}
	return &Adapter{client: client, policy: &policy}
}

// CloseIdleConnections is used after authentication and rate-limit failures.
// Some provider edge servers answer before consuming the request body; closing
// the idle connection prevents a subsequent request from inheriting that state.
func (a *Adapter) CloseIdleConnections() { a.client.CloseIdleConnections() }

func (a *Adapter) Forward(ctx context.Context, in providers.ForwardRequest) (*http.Response, error) {
	base, err := url.Parse(strings.TrimRight(in.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid provider base URL: %w", err)
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, errors.New("provider base URL must use http or https")
	}
	if a.policy != nil {
		if err = a.policy.ValidateURL(base); err != nil {
			return nil, err
		}
	}
	path := "/" + strings.TrimLeft(in.Path, "/")
	target := base.ResolveReference(&url.URL{Path: strings.TrimRight(base.Path, "/") + path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), in.Body)
	if err != nil {
		return nil, err
	}
	ct := in.ContentType
	if ct == "" {
		ct = "application/json"
	}
	req.Header.Set("Content-Type", ct)
	if in.Accept != "" {
		req.Header.Set("Accept", in.Accept)
	}
	req.Header.Set("User-Agent", "RelayDock/1.0")
	if in.ClientRequestID != "" {
		req.Header.Set("X-Client-Request-Id", in.ClientRequestID)
	}
	if in.Traceparent != "" {
		req.Header.Set("traceparent", in.Traceparent)
	}
	req.Header.Set("Authorization", "Bearer "+in.Credential.Secret)
	if in.Credential.OrganizationID != "" {
		req.Header.Set("OpenAI-Organization", in.Credential.OrganizationID)
	}
	if in.Credential.ProjectID != "" {
		req.Header.Set("OpenAI-Project", in.Credential.ProjectID)
	}
	return a.client.Do(req)
}
func (a *Adapter) CreateResponse(ctx context.Context, r providers.ForwardRequest) (*http.Response, error) {
	r.Path = "/responses"
	return a.Forward(ctx, r)
}
func (a *Adapter) CreateChatCompletion(ctx context.Context, r providers.ForwardRequest) (*http.Response, error) {
	r.Path = "/chat/completions"
	return a.Forward(ctx, r)
}
func (a *Adapter) CreateStreamCompletion(ctx context.Context, r providers.ForwardRequest) (*http.Response, error) {
	r.Path = "/chat/completions"
	r.Accept = "text/event-stream"
	return a.Forward(ctx, r)
}
func (a *Adapter) CreateEmbedding(ctx context.Context, r providers.ForwardRequest) (*http.Response, error) {
	r.Path = "/embeddings"
	return a.Forward(ctx, r)
}

func (a *Adapter) ListModels(ctx context.Context, baseURL string, c providers.Credential) ([]providers.Model, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
	}
	if a.policy != nil {
		if err = a.policy.ValidateURL(base); err != nil {
			return nil, err
		}
	}
	target := base.ResolveReference(&url.URL{Path: strings.TrimRight(base.Path, "/") + "/models"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("User-Agent", "RelayDock/1.0")
	if c.OrganizationID != "" {
		req.Header.Set("OpenAI-Organization", c.OrganizationID)
	}
	if c.ProjectID != "" {
		req.Header.Set("OpenAI-Project", c.ProjectID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}
	var payload struct {
		Data []providers.Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}
func (a *Adapter) HealthCheck(ctx context.Context, baseURL string, c providers.Credential) error {
	_, err := a.ListModels(ctx, baseURL, c)
	return err
}

func ForwardResponseHeader(dst http.Header, src http.Header) {
	for _, name := range []string{"Content-Type", "Cache-Control", "OpenAI-Version"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}

func (p EndpointPolicy) ValidateURL(target *url.URL) error {
	if target == nil || target.Hostname() == "" || target.User != nil {
		return errors.New("provider base URL must be absolute and must not contain user information")
	}
	if target.Scheme != "https" && !(p.AllowHTTP && target.Scheme == "http") {
		return errors.New("provider base URL must use HTTPS")
	}
	if !p.hostAllowed(target.Hostname()) {
		return errors.New("provider endpoint host is not allowlisted")
	}
	return nil
}

func (p EndpointPolicy) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, candidate := range p.AllowedHosts {
		candidate = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(candidate), "."))
		if candidate == host {
			return true
		}
		if strings.HasPrefix(candidate, "*.") && strings.HasSuffix(host, candidate[1:]) && host != candidate[2:] {
			return true
		}
	}
	return false
}

func (p EndpointPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !p.hostAllowed(host) {
		return nil, errors.New("provider endpoint address is not allowlisted")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("provider endpoint could not be resolved")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		if !p.AllowPrivateNetwork && unsafeProviderIP(ip) {
			lastErr = errors.New("provider endpoint resolves to a private or local address")
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("provider endpoint has no allowed address")
	}
	return nil, lastErr
}

func unsafeProviderIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
