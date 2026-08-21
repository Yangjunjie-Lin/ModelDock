package payment

import (
	"context"
)

type ManualTransferAdapter struct {
	enabled        bool
	allowedRegions []string
}

func NewManualTransfer(enabled bool, allowedRegions []string) *ManualTransferAdapter {
	return &ManualTransferAdapter{enabled: enabled, allowedRegions: append([]string(nil), allowedRegions...)}
}

func (adapter *ManualTransferAdapter) Capabilities() Capabilities {
	return Capabilities{Name: "manual_transfer", Enabled: adapter.enabled, ContractStatus: "INTERNAL_APPROVED",
		AllowedRegions: append([]string(nil), adapter.allowedRegions...), ProductionReady: false,
		SupportsWebhook: false, SupportsRefund: true, SupportsClose: true, SupportsReconcile: false}
}

func (adapter *ManualTransferAdapter) CreatePayment(_ context.Context, request CreateRequest) (PaymentResult, error) {
	expires := request.ExpiresAt
	return PaymentResult{ProviderOrderNo: "manual_" + request.PlatformOrderNo, Status: "PENDING", Amount: request.Amount,
		Currency: request.Currency, ExpiresAt: &expires, Instructions: map[string]any{
			"mode": "manual_transfer", "message": "Transfer instructions are supplied out-of-band by an authorized administrator; approval is required.",
		}}, nil
}

func (adapter *ManualTransferAdapter) QueryPayment(_ context.Context, request QueryRequest) (PaymentResult, error) {
	return PaymentResult{ProviderOrderNo: request.ProviderOrderNo, Status: "PENDING"}, nil
}

func (adapter *ManualTransferAdapter) VerifyWebhook(context.Context, WebhookRequest) (VerifiedWebhook, error) {
	return VerifiedWebhook{}, ErrWebhookUnsupported
}

func (adapter *ManualTransferAdapter) RefundPayment(_ context.Context, request RefundRequest) (RefundResult, error) {
	return RefundResult{ProviderRefundNo: "manual_refund_" + request.PlatformRefundNo, Status: "PENDING", RequiresReview: true}, nil
}

func (adapter *ManualTransferAdapter) ClosePayment(_ context.Context, request QueryRequest) (PaymentResult, error) {
	return PaymentResult{ProviderOrderNo: request.ProviderOrderNo, Status: "EXPIRED"}, nil
}

func (adapter *ManualTransferAdapter) ReconcilePayment(_ context.Context, request ReconcileRequest) (ReconcileResult, error) {
	return ReconcileResult{}, ErrReconcileUnsupported
}
