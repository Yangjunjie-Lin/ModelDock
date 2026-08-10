package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

type Credential struct {
	Secret         string
	OrganizationID string
	ProjectID      string
}
type ForwardRequest struct {
	BaseURL         string
	Path            string
	Body            io.Reader
	ContentType     string
	Accept          string
	ClientRequestID string
	Credential      Credential
}
type Provider interface {
	Forward(context.Context, ForwardRequest) (*http.Response, error)
	ListModels(context.Context, string, Credential) ([]Model, error)
	CreateResponse(context.Context, ForwardRequest) (*http.Response, error)
	CreateChatCompletion(context.Context, ForwardRequest) (*http.Response, error)
	CreateStreamCompletion(context.Context, ForwardRequest) (*http.Response, error)
	CreateEmbedding(context.Context, ForwardRequest) (*http.Response, error)
	HealthCheck(context.Context, string, Credential) error
}

var ErrProviderNotRegistered = errors.New("provider adapter is not registered")

// Registry resolves a provider_type to a runtime adapter. It is safe for
// concurrent gateway reads and supports adding adapters without changing the
// data-plane handler.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Provider
}

func NewRegistry() *Registry { return &Registry{adapters: make(map[string]Provider)} }

func (r *Registry) Register(providerType string, adapter Provider) {
	if r == nil || adapter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[strings.ToLower(strings.TrimSpace(providerType))] = adapter
}

func (r *Registry) Resolve(providerType string) (Provider, error) {
	if r == nil {
		return nil, ErrProviderNotRegistered
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[strings.ToLower(strings.TrimSpace(providerType))]
	if !ok {
		return nil, ErrProviderNotRegistered
	}
	return adapter, nil
}

type IdleConnectionCloser interface{ CloseIdleConnections() }
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}
