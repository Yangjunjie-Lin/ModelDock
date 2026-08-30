package provisioning

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/relayedock/relayedock/internal/domain"
)

var (
	ErrNotRegistered        = errors.New("provider provisioning adapter is not registered")
	ErrAutomaticUnsupported = errors.New("provider does not support this official automatic provisioning operation")
	ErrBindingMismatch      = errors.New("provider binding does not belong to the requested user and provider")
)

// Capability is deliberately explicit. An integration can support automatic
// project/service-account creation without claiming that the upstream offers a
// project-wallet credit API.
type Capability struct {
	ProviderType             string `json:"provider_type"`
	Mode                     string `json:"mode"`
	Enabled                  bool   `json:"enabled"`
	SupportsAutomaticBinding bool   `json:"supports_automatic_binding"`
	SupportsAutomaticCredit  bool   `json:"supports_automatic_credit"`
	SupportsRefresh          bool   `json:"supports_refresh"`
	FreeTestModel            string `json:"free_test_model,omitempty"`
	Reason                   string `json:"reason,omitempty"`
	DocumentationURL         string `json:"documentation_url,omitempty"`
}

type BindingRequest struct {
	BindingID         string
	OrganizationID    string
	UserID            string
	ProviderID        string
	IdempotencyKey    string
	ExternalAccountID string
	ExternalProjectID string
}

type BindingResult struct {
	ExternalAccountID string
	ExternalProjectID string
	CredentialSecret  string
	CredentialType    string
	CredentialName    string
	Metadata          map[string]any
}

type AllocationRequest struct {
	BindingID         string
	OrganizationID    string
	UserID            string
	ProviderID        string
	ExternalAccountID string
	ExternalProjectID string
	IdempotencyKey    string
	Amount            domain.Decimal
	Currency          string
}

type AllocationResult struct {
	ExternalReference string
	Metadata          map[string]any
}

type Provisioner interface {
	Capabilities() Capability
	EnsureBinding(context.Context, BindingRequest) (BindingResult, error)
	AllocateCredit(context.Context, AllocationRequest) (AllocationResult, error)
	RefreshBinding(context.Context, BindingRequest) (BindingResult, error)
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Provisioner
}

func NewRegistry() *Registry { return &Registry{adapters: make(map[string]Provisioner)} }

func (registry *Registry) Register(providerType string, adapter Provisioner) {
	if registry == nil || adapter == nil {
		return
	}
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "" {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.adapters[providerType] = adapter
}

func (registry *Registry) Resolve(providerType string) (Provisioner, error) {
	if registry == nil {
		return nil, ErrNotRegistered
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	adapter, ok := registry.adapters[strings.ToLower(strings.TrimSpace(providerType))]
	if !ok {
		return nil, ErrNotRegistered
	}
	return adapter, nil
}

func (registry *Registry) Capability(providerType string) Capability {
	adapter, err := registry.Resolve(providerType)
	if err == nil {
		return adapter.Capabilities()
	}
	return Capability{ProviderType: strings.ToLower(strings.TrimSpace(providerType)), Mode: "MANUAL", Enabled: true,
		Reason: "No official project or sub-account API is configured; use a reviewed manual binding or BYOK."}
}

func (registry *Registry) List() []Capability {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]Capability, 0, len(registry.adapters))
	for _, adapter := range registry.adapters {
		out = append(out, adapter.Capabilities())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderType < out[j].ProviderType })
	return out
}

// Static is used for provider families whose supported onboarding path is
// BYOK or operator review. It never pretends to create an upstream account.
type Static struct{ capability Capability }

func NewStatic(capability Capability) *Static    { return &Static{capability: capability} }
func (adapter *Static) Capabilities() Capability { return adapter.capability }
func (adapter *Static) EnsureBinding(context.Context, BindingRequest) (BindingResult, error) {
	return BindingResult{}, ErrAutomaticUnsupported
}
func (adapter *Static) AllocateCredit(context.Context, AllocationRequest) (AllocationResult, error) {
	return AllocationResult{}, ErrAutomaticUnsupported
}
func (adapter *Static) RefreshBinding(context.Context, BindingRequest) (BindingResult, error) {
	return BindingResult{}, ErrAutomaticUnsupported
}
