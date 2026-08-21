// Package payout defines the contract and region governed boundary between
// approved supplier settlements and external payout processors.
package payout

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/relayedock/relayedock/internal/domain"
)

var (
	ErrAdapterNotRegistered = errors.New("payout adapter is not registered")
	ErrAdapterDisabled      = errors.New("payout adapter is disabled")
	ErrContractUnavailable  = errors.New("payout adapter contract is not active")
	ErrRegionNotAllowed     = errors.New("payout adapter is not allowed in this region")
)

type Capabilities struct {
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	ContractStatus  string   `json:"contract_status"`
	AllowedRegions  []string `json:"allowed_regions"`
	ProductionReady bool     `json:"production_ready"`
}

type Request struct {
	SettlementID   string
	IdempotencyKey string
	Amount         domain.Decimal
	Currency       string
	Region         string
	Destination    string
}

type Result struct {
	ProviderReference string         `json:"provider_reference"`
	Status            string         `json:"status"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

// Adapter implementations must pass IdempotencyKey unchanged to their
// processor. ModelDock can repeat Send after an ambiguous response or crash.
type Adapter interface {
	Capabilities() Capabilities
	Send(context.Context, Request) (Result, error)
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry { return &Registry{adapters: make(map[string]Adapter)} }

func (registry *Registry) Register(adapter Adapter) {
	if registry == nil || adapter == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(adapter.Capabilities().Name))
	if name == "" {
		return
	}
	registry.mu.Lock()
	registry.adapters[name] = adapter
	registry.mu.Unlock()
}

func (registry *Registry) Resolve(name, region string) (Adapter, error) {
	if registry == nil {
		return nil, ErrAdapterNotRegistered
	}
	registry.mu.RLock()
	adapter := registry.adapters[strings.ToLower(strings.TrimSpace(name))]
	registry.mu.RUnlock()
	if adapter == nil {
		return nil, ErrAdapterNotRegistered
	}
	capabilities := adapter.Capabilities()
	if !capabilities.Enabled {
		return nil, ErrAdapterDisabled
	}
	if capabilities.ContractStatus != "TEST_ONLY" && capabilities.ContractStatus != "INTERNAL_APPROVED" && capabilities.ContractStatus != "ACTIVE" {
		return nil, ErrContractUnavailable
	}
	if capabilities.ContractStatus == "ACTIVE" && !capabilities.ProductionReady {
		return nil, ErrContractUnavailable
	}
	region = strings.ToUpper(strings.TrimSpace(region))
	for _, allowed := range capabilities.AllowedRegions {
		if strings.EqualFold(region, allowed) {
			return adapter, nil
		}
	}
	return nil, ErrRegionNotAllowed
}

func (registry *Registry) List() []Capabilities {
	if registry == nil {
		return []Capabilities{}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]Capabilities, 0, len(registry.adapters))
	for _, adapter := range registry.adapters {
		out = append(out, adapter.Capabilities())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
