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

type Adapter struct{ client *http.Client }

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
func (a *Adapter) CreateEmbedding(ctx context.Context, r providers.ForwardRequest) (*http.Response, error) {
	r.Path = "/embeddings"
	return a.Forward(ctx, r)
}

func (a *Adapter) ListModels(ctx context.Context, baseURL string, c providers.Credential) ([]providers.Model, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
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
