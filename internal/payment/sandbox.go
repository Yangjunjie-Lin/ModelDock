package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

type SandboxConfig struct {
	Enabled        bool
	Secret         []byte
	AllowedRegions []string
	TimestampSkew  time.Duration
}

type SandboxAdapter struct {
	config SandboxConfig
	mu     sync.RWMutex
	values map[string]sandboxState
}

type sandboxState struct {
	result PaymentResult
}

func NewSandbox(config SandboxConfig) *SandboxAdapter {
	if config.TimestampSkew <= 0 {
		config.TimestampSkew = 5 * time.Minute
	}
	return &SandboxAdapter{config: config, values: make(map[string]sandboxState)}
}

func (adapter *SandboxAdapter) Capabilities() Capabilities {
	return Capabilities{Name: "sandbox", Enabled: adapter.config.Enabled && len(adapter.config.Secret) >= 32,
		ContractStatus: "TEST_ONLY", AllowedRegions: append([]string(nil), adapter.config.AllowedRegions...),
		ProductionReady: false, SupportsWebhook: true, SupportsRefund: true, SupportsClose: true, SupportsReconcile: true}
}

func (adapter *SandboxAdapter) CreatePayment(_ context.Context, request CreateRequest) (PaymentResult, error) {
	expires := request.ExpiresAt
	result := PaymentResult{ProviderOrderNo: "sbx_" + request.PlatformOrderNo, Status: "PENDING", Amount: request.Amount,
		Currency: request.Currency, ExpiresAt: &expires, Instructions: map[string]any{
			"mode": "sandbox", "message": "Use a signed sandbox webhook; this adapter never moves real funds.",
		}}
	adapter.mu.Lock()
	adapter.values[result.ProviderOrderNo] = sandboxState{result: result}
	adapter.mu.Unlock()
	return result, nil
}

func (adapter *SandboxAdapter) QueryPayment(_ context.Context, request QueryRequest) (PaymentResult, error) {
	adapter.mu.RLock()
	state, ok := adapter.values[request.ProviderOrderNo]
	adapter.mu.RUnlock()
	if ok {
		return state.result, nil
	}
	return PaymentResult{ProviderOrderNo: request.ProviderOrderNo, Status: "PENDING"}, nil
}

func (adapter *SandboxAdapter) VerifyWebhook(_ context.Context, request WebhookRequest) (VerifiedWebhook, error) {
	timestampSeconds, err := strconv.ParseInt(strings.TrimSpace(request.Timestamp), 10, 64)
	if err != nil {
		return VerifiedWebhook{}, ErrTimestampInvalid
	}
	providerTimestamp := time.Unix(timestampSeconds, 0).UTC()
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	delta := now.Sub(providerTimestamp)
	if delta < 0 {
		delta = -delta
	}
	if delta > adapter.config.TimestampSkew {
		return VerifiedWebhook{}, ErrTimestampInvalid
	}
	mac := hmac.New(sha256.New, adapter.config.Secret)
	_, _ = mac.Write([]byte(strings.TrimSpace(request.Timestamp)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(request.Body)
	expected := mac.Sum(nil)
	provided, err := hex.DecodeString(strings.TrimSpace(request.Signature))
	if err != nil || !hmac.Equal(provided, expected) {
		return VerifiedWebhook{}, ErrInvalidSignature
	}
	var payload struct {
		EventID         string         `json:"event_id"`
		EventType       string         `json:"event_type"`
		PlatformOrderNo string         `json:"platform_order_no"`
		ProviderOrderNo string         `json:"provider_order_no"`
		Status          string         `json:"status"`
		Amount          domain.Decimal `json:"amount"`
		Currency        string         `json:"currency"`
	}
	if err = json.Unmarshal(request.Body, &payload); err != nil {
		return VerifiedWebhook{}, errors.New("invalid sandbox webhook body")
	}
	if request.EventID != "" && !hmac.Equal([]byte(request.EventID), []byte(payload.EventID)) {
		return VerifiedWebhook{}, ErrInvalidSignature
	}
	payload.Status = strings.ToUpper(strings.TrimSpace(payload.Status))
	if payload.EventID == "" || payload.PlatformOrderNo == "" || payload.ProviderOrderNo == "" ||
		(payload.Status != "PAID" && payload.Status != "FAILED" && payload.Status != "CHARGEBACK") ||
		!payload.Amount.IsPositive() || len(payload.Currency) != 3 {
		return VerifiedWebhook{}, errors.New("invalid sandbox webhook fields")
	}
	verified := VerifiedWebhook{ProviderEventID: payload.EventID, ProviderOrderNo: payload.ProviderOrderNo,
		PlatformOrderNo: payload.PlatformOrderNo, EventType: payload.EventType, Status: payload.Status,
		Amount: payload.Amount, Currency: strings.ToUpper(payload.Currency), ProviderTimestamp: providerTimestamp,
		ReplayKey: payload.EventID, NormalizedPayload: map[string]any{
			"event_id": payload.EventID, "event_type": payload.EventType, "status": payload.Status,
		}}
	return verified, nil
}

func (adapter *SandboxAdapter) RefundPayment(_ context.Context, request RefundRequest) (RefundResult, error) {
	result := RefundResult{ProviderRefundNo: "sbxr_" + request.PlatformRefundNo, Status: "SUCCEEDED"}
	adapter.mu.Lock()
	adapter.values[result.ProviderRefundNo] = sandboxState{result: PaymentResult{ProviderOrderNo: result.ProviderRefundNo,
		Status: result.Status, Amount: request.Amount, Currency: request.Currency}}
	adapter.mu.Unlock()
	return result, nil
}

func (adapter *SandboxAdapter) ClosePayment(_ context.Context, request QueryRequest) (PaymentResult, error) {
	result := PaymentResult{ProviderOrderNo: request.ProviderOrderNo, Status: "EXPIRED"}
	adapter.mu.Lock()
	state := adapter.values[request.ProviderOrderNo]
	result.Amount, result.Currency = state.result.Amount, state.result.Currency
	state.result = result
	adapter.values[request.ProviderOrderNo] = state
	adapter.mu.Unlock()
	return result, nil
}

func (adapter *SandboxAdapter) ReconcilePayment(_ context.Context, request ReconcileRequest) (ReconcileResult, error) {
	adapter.mu.RLock()
	state, ok := adapter.values[request.ProviderOrderNo]
	adapter.mu.RUnlock()
	if ok {
		return ReconcileResult{ProviderStatus: state.result.Status, Amount: state.result.Amount, Currency: state.result.Currency,
			EvidenceSource: "PROVIDER_API",
			Details:        map[string]any{"provider_order_no": request.ProviderOrderNo, "mode": "sandbox"}}, nil
	}
	if strings.HasPrefix(request.ProviderOrderNo, "sbxr_") {
		return ReconcileResult{ProviderStatus: "SUCCEEDED", Amount: request.Amount, Currency: request.Currency,
			EvidenceSource: "PROVIDER_API",
			Details:        map[string]any{"provider_order_no": request.ProviderOrderNo, "mode": "sandbox", "recovered": true}}, nil
	}
	return ReconcileResult{}, ErrReconcileUnsupported
}

// RememberVerifiedWebhook is called only after ModelDock has durably accepted
// the signed event. It keeps the process-local sandbox query simulation in
// sync without allowing an invalid local-order match to mutate adapter state.
func (adapter *SandboxAdapter) RememberVerifiedWebhook(verified VerifiedWebhook) {
	adapter.mu.Lock()
	adapter.values[verified.ProviderOrderNo] = sandboxState{result: PaymentResult{ProviderOrderNo: verified.ProviderOrderNo,
		Status: verified.Status, Amount: verified.Amount, Currency: verified.Currency}}
	adapter.mu.Unlock()
}
