package payment

import (
	"context"
	"log/slog"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

type RecoveryStore interface {
	ListRecoverableRechargeOrders(context.Context, int) ([]domain.RechargeOrder, error)
	CreditPaidRecharge(context.Context, string) (domain.RechargeOrder, bool, error)
	ExpireRechargeOrders(context.Context, time.Time, int) ([]domain.RechargeOrder, error)
	ListUnclosedExpiredRechargeOrders(context.Context, int) ([]domain.RechargeOrder, error)
	MarkRechargeProviderClosed(context.Context, string) error
	StartPaymentAttempt(context.Context, string, string, string, map[string]any) (domain.PaymentAttempt, error)
	FinishPaymentAttempt(context.Context, string, string, string, string, string, map[string]any) error
	ListRecoverableRefundOrders(context.Context, int) ([]domain.RefundOrder, error)
	RechargeOrderByID(context.Context, string) (domain.RechargeOrder, error)
	CompleteRefund(context.Context, string, string) (domain.RefundOrder, bool, error)
}

type Worker struct {
	store        RecoveryStore
	registry     *Registry
	pollInterval time.Duration
	logger       *slog.Logger
}

func NewWorker(store RecoveryStore, registry *Registry, pollInterval time.Duration, logger *slog.Logger) *Worker {
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, registry: registry, pollInterval: pollInterval, logger: logger}
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.store == nil || worker.registry == nil {
		return
	}
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	worker.recover(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.recover(ctx)
		}
	}
}

func (worker *Worker) recover(ctx context.Context) {
	orders, err := worker.store.ListRecoverableRechargeOrders(ctx, 100)
	if err != nil {
		worker.logger.Error("payment_credit_recovery_failed", "error", err)
		return
	}
	for _, order := range orders {
		if _, _, err = worker.store.CreditPaidRecharge(ctx, order.ID); err != nil {
			worker.logger.Error("payment_credit_failed", "order_id", order.ID, "error", err)
		}
	}
	_, err = worker.store.ExpireRechargeOrders(ctx, time.Now().UTC(), 100)
	if err != nil {
		worker.logger.Error("payment_expiry_failed", "error", err)
		return
	}
	expired, err := worker.store.ListUnclosedExpiredRechargeOrders(ctx, 100)
	if err != nil {
		worker.logger.Error("payment_expiry_close_scan_failed", "error", err)
		return
	}
	for _, order := range expired {
		provider, resolveErr := worker.registry.Resolve(order.PaymentProvider, order.Region)
		if resolveErr != nil || !provider.Capabilities().SupportsClose {
			continue
		}
		attempt, startErr := worker.store.StartPaymentAttempt(ctx, order.ID, "CLOSE", "", map[string]any{"source": "expiry_worker"})
		if startErr != nil {
			continue
		}
		result, closeErr := provider.ClosePayment(ctx, QueryRequest{PlatformOrderNo: order.PlatformOrderNo, ProviderOrderNo: order.ProviderOrderNo})
		if closeErr != nil {
			_ = worker.store.FinishPaymentAttempt(ctx, attempt.ID, "FAILED", order.ProviderOrderNo, "", "close_failed", nil)
			continue
		}
		_ = worker.store.FinishPaymentAttempt(ctx, attempt.ID, "SUCCEEDED", result.ProviderOrderNo, result.Status, "", nil)
		_ = worker.store.MarkRechargeProviderClosed(ctx, order.ID)
	}
	refunds, err := worker.store.ListRecoverableRefundOrders(ctx, 100)
	if err != nil {
		worker.logger.Error("payment_refund_recovery_failed", "error", err)
		return
	}
	for _, refund := range refunds {
		if refund.ProviderRefundNo == "" {
			continue
		}
		order, loadErr := worker.store.RechargeOrderByID(ctx, refund.RechargeOrderID)
		if loadErr != nil {
			continue
		}
		provider, resolveErr := worker.registry.Resolve(order.PaymentProvider, order.Region)
		if resolveErr != nil {
			continue
		}
		attempt, startErr := worker.store.StartPaymentAttempt(ctx, order.ID, "RECONCILE", "", map[string]any{
			"source": "refund_recovery", "refund_order_id": refund.ID,
		})
		if startErr != nil {
			continue
		}
		result, reconcileErr := provider.ReconcilePayment(ctx, ReconcileRequest{PlatformOrderNo: order.PlatformOrderNo,
			ProviderOrderNo: refund.ProviderRefundNo, LocalStatus: refund.Status, Amount: refund.Amount, Currency: refund.Currency})
		if reconcileErr != nil {
			_ = worker.store.FinishPaymentAttempt(ctx, attempt.ID, "FAILED", order.ProviderOrderNo, "", "refund_reconcile_failed", nil)
			continue
		}
		_ = worker.store.FinishPaymentAttempt(ctx, attempt.ID, "SUCCEEDED", order.ProviderOrderNo, result.ProviderStatus, "", map[string]any{
			"refund_order_id": refund.ID, "provider_refund_no": refund.ProviderRefundNo,
		})
		if reconcileErr == nil && result.ProviderStatus == "SUCCEEDED" {
			_, _, _ = worker.store.CompleteRefund(ctx, refund.ID, refund.ProviderRefundNo)
		}
	}
}
