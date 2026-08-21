// Package payment defines the signed, contract-aware boundary between
// ModelDock's durable recharge workflow and payment-channel adapters.
package payment

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

var (
	ErrProviderNotRegistered = errors.New("payment provider is not registered")
	ErrProviderDisabled      = errors.New("payment provider is disabled")
	ErrContractUnavailable   = errors.New("payment provider contract is not active")
	ErrRegionNotAllowed      = errors.New("payment provider is not allowed in this region")
	ErrWebhookUnsupported    = errors.New("payment provider does not accept webhooks")
	ErrInvalidSignature      = errors.New("payment webhook signature is invalid")
	ErrTimestampInvalid      = errors.New("payment webhook timestamp is outside the allowed window")
	ErrRefundRequiresReview  = errors.New("payment refund requires administrator review")
	ErrReconcileUnsupported  = errors.New("payment provider does not support independent reconciliation evidence")
)

type Capabilities struct {
	Name              string   `json:"name"`
	Enabled           bool     `json:"enabled"`
	ContractStatus    string   `json:"contract_status"`
	AllowedRegions    []string `json:"allowed_regions"`
	ProductionReady   bool     `json:"production_ready"`
	SupportsWebhook   bool     `json:"supports_webhook"`
	SupportsRefund    bool     `json:"supports_refund"`
	SupportsClose     bool     `json:"supports_close"`
	SupportsReconcile bool     `json:"supports_reconcile"`
}

type CreateRequest struct {
	PlatformOrderNo string
	Amount          domain.Decimal
	Currency        string
	Region          string
	ExpiresAt       time.Time
}

type PaymentResult struct {
	ProviderOrderNo string         `json:"provider_order_no"`
	Status          string         `json:"status"`
	Amount          domain.Decimal `json:"amount"`
	Currency        string         `json:"currency"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
	Instructions    map[string]any `json:"instructions,omitempty"`
}

type QueryRequest struct {
	PlatformOrderNo string
	ProviderOrderNo string
}

type WebhookRequest struct {
	Body      []byte
	Signature string
	Timestamp string
	EventID   string
	Now       time.Time
}

type VerifiedWebhook struct {
	ProviderEventID   string         `json:"provider_event_id"`
	ProviderOrderNo   string         `json:"provider_order_no"`
	PlatformOrderNo   string         `json:"platform_order_no"`
	EventType         string         `json:"event_type"`
	Status            string         `json:"status"`
	Amount            domain.Decimal `json:"amount"`
	Currency          string         `json:"currency"`
	ProviderTimestamp time.Time      `json:"provider_timestamp"`
	ReplayKey         string         `json:"-"`
	NormalizedPayload map[string]any `json:"normalized_payload"`
}

type RefundRequest struct {
	PlatformOrderNo  string
	ProviderOrderNo  string
	PlatformRefundNo string
	Amount           domain.Decimal
	Currency         string
	Reason           string
}

// Implementations must treat PlatformOrderNo (create) and PlatformRefundNo
// (refund) as provider-side idempotency keys. ModelDock may repeat either call
// after an ambiguous network failure or process exit.

type RefundResult struct {
	ProviderRefundNo string `json:"provider_refund_no"`
	Status           string `json:"status"`
	RequiresReview   bool   `json:"requires_review"`
}

type ReconcileRequest struct {
	PlatformOrderNo string
	ProviderOrderNo string
	LocalStatus     string
	Amount          domain.Decimal
	Currency        string
}

type ReconcileResult struct {
	ProviderStatus string         `json:"provider_status"`
	Amount         domain.Decimal `json:"amount"`
	Currency       string         `json:"currency"`
	EvidenceSource string         `json:"evidence_source"`
	Details        map[string]any `json:"details,omitempty"`
}

// Provider is the only extension point formal payment plugins implement.
// Payment secrets remain inside adapters and are injected at process startup.
type Provider interface {
	Capabilities() Capabilities
	CreatePayment(context.Context, CreateRequest) (PaymentResult, error)
	QueryPayment(context.Context, QueryRequest) (PaymentResult, error)
	VerifyWebhook(context.Context, WebhookRequest) (VerifiedWebhook, error)
	RefundPayment(context.Context, RefundRequest) (RefundResult, error)
	ClosePayment(context.Context, QueryRequest) (PaymentResult, error)
	ReconcilePayment(context.Context, ReconcileRequest) (ReconcileResult, error)
}

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry { return &Registry{providers: make(map[string]Provider)} }

func (registry *Registry) Register(provider Provider) {
	if registry == nil || provider == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(provider.Capabilities().Name))
	if name == "" {
		return
	}
	registry.mu.Lock()
	registry.providers[name] = provider
	registry.mu.Unlock()
}

func (registry *Registry) Resolve(name, region string) (Provider, error) {
	if registry == nil {
		return nil, ErrProviderNotRegistered
	}
	registry.mu.RLock()
	provider := registry.providers[strings.ToLower(strings.TrimSpace(name))]
	registry.mu.RUnlock()
	if provider == nil {
		return nil, ErrProviderNotRegistered
	}
	capabilities := provider.Capabilities()
	if !capabilities.Enabled {
		return nil, ErrProviderDisabled
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
			return provider, nil
		}
	}
	return nil, ErrRegionNotAllowed
}

// ResolveWebhook checks the adapter switch and contract before verification.
// The order region is checked again after the signed platform order number has
// been resolved from the webhook payload.
func (registry *Registry) ResolveWebhook(name string) (Provider, error) {
	if registry == nil {
		return nil, ErrProviderNotRegistered
	}
	registry.mu.RLock()
	provider := registry.providers[strings.ToLower(strings.TrimSpace(name))]
	registry.mu.RUnlock()
	if provider == nil {
		return nil, ErrProviderNotRegistered
	}
	capabilities := provider.Capabilities()
	if !capabilities.Enabled {
		return nil, ErrProviderDisabled
	}
	if capabilities.ContractStatus != "TEST_ONLY" && capabilities.ContractStatus != "INTERNAL_APPROVED" && capabilities.ContractStatus != "ACTIVE" {
		return nil, ErrContractUnavailable
	}
	if capabilities.ContractStatus == "ACTIVE" && !capabilities.ProductionReady {
		return nil, ErrContractUnavailable
	}
	if !capabilities.SupportsWebhook {
		return nil, ErrWebhookUnsupported
	}
	return provider, nil
}

func (registry *Registry) List() []Capabilities {
	if registry == nil {
		return []Capabilities{}
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]Capabilities, 0, len(registry.providers))
	for _, provider := range registry.providers {
		out = append(out, provider.Capabilities())
	}
	sort.Slice(out, func(left, right int) bool { return out[left].Name < out[right].Name })
	return out
}
