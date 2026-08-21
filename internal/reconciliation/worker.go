// Package reconciliation schedules the daily, replay-safe financial close.
// The database run key serializes replicas and prevents duplicate runs or
// corrections for the same UTC business date.
package reconciliation

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/payment"
)

type Store interface {
	RunFinancialReconciliation(context.Context, time.Time, string, *string) (domain.ReconciliationRun, bool, error)
}

type PaymentStore interface {
	Store
	ListRechargeOrdersForReconciliation(context.Context, time.Time, string, int) ([]domain.RechargeOrder, error)
	RecordReconciliation(context.Context, domain.RechargeOrder, string, payment.ReconcileResult, *string) (domain.PaymentReconciliationRecord, error)
	RecordReconciliationFailure(context.Context, domain.RechargeOrder, string, string, map[string]any) (domain.PaymentReconciliationRecord, error)
}

type Worker struct {
	store        Store
	interval     time.Duration
	runAt        time.Duration
	logger       *slog.Logger
	now          func() time.Time
	payments     *payment.Registry
	paymentStore PaymentStore
}

// NewWorker accepts runAt as an offset after UTC midnight. The interval is a
// lightweight wake-up cadence; the durable daily run key is the idempotency
// boundary across retries and multiple replicas.
func NewWorker(store Store, interval, runAt time.Duration, logger *slog.Logger) *Worker {
	return newWorker(store, nil, nil, interval, runAt, logger)
}

// NewWorkerWithPayments adds the independent payment-channel evidence pass
// immediately before the database close. NewWorker remains as a compatibility
// constructor for tests and embedders that do not configure payment adapters.
func NewWorkerWithPayments(store PaymentStore, payments *payment.Registry, interval, runAt time.Duration, logger *slog.Logger) *Worker {
	return newWorker(store, store, payments, interval, runAt, logger)
}

func newWorker(store Store, paymentStore PaymentStore, payments *payment.Registry, interval, runAt time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	if runAt < 0 || runAt >= 24*time.Hour {
		runAt = 2 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, interval: interval, runAt: runAt, logger: logger, now: time.Now, payments: payments, paymentStore: paymentStore}
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.store == nil {
		return
	}
	worker.reconcile(ctx, worker.now().UTC())
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			worker.reconcile(ctx, now.UTC())
		}
	}
}

func (worker *Worker) reconcile(ctx context.Context, now time.Time) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if now.Before(today.Add(worker.runAt)) {
		return
	}
	businessDate := today.AddDate(0, 0, -1)
	if worker.payments != nil {
		if err := worker.reconcilePaymentChannels(ctx, businessDate); err != nil {
			worker.logger.Error("payment_channel_reconciliation_failed", "business_date", businessDate.Format("2006-01-02"), "error", err)
			return
		}
	}
	run, replayed, err := worker.store.RunFinancialReconciliation(ctx, businessDate, "SCHEDULED", nil)
	if err != nil {
		worker.logger.Error("financial_reconciliation_failed", "business_date", businessDate.Format("2006-01-02"), "error", err)
		return
	}
	if !replayed {
		worker.logger.Info("financial_reconciliation_completed", "run_id", run.ID, "business_date", run.BusinessDate, "summary", run.Summary)
	}
}

func (worker *Worker) reconcilePaymentChannels(ctx context.Context, businessDate time.Time) error {
	const pageSize = 100
	cursor := ""
	date := businessDate.UTC().Format("2006-01-02")
	for {
		orders, err := worker.paymentStore.ListRechargeOrdersForReconciliation(ctx, businessDate, cursor, pageSize)
		if err != nil {
			return err
		}
		for _, order := range orders {
			key := "daily:" + date + ":" + order.ID
			adapter, resolveErr := worker.payments.Resolve(order.PaymentProvider, order.Region)
			if resolveErr != nil {
				if _, err = worker.paymentStore.RecordReconciliationFailure(ctx, order, key, paymentReconciliationErrorCode(resolveErr), map[string]any{
					"phase": "resolve", "payment_provider": order.PaymentProvider,
				}); err != nil {
					return err
				}
				continue
			}
			if !adapter.Capabilities().SupportsReconcile {
				if _, err = worker.paymentStore.RecordReconciliationFailure(ctx, order, key, "reconciliation_unsupported", map[string]any{
					"phase": "capability", "payment_provider": order.PaymentProvider,
				}); err != nil {
					return err
				}
				continue
			}
			result, reconcileErr := adapter.ReconcilePayment(ctx, payment.ReconcileRequest{PlatformOrderNo: order.PlatformOrderNo,
				ProviderOrderNo: order.ProviderOrderNo, LocalStatus: order.Status, Amount: order.Amount, Currency: order.Currency})
			if reconcileErr != nil {
				if _, err = worker.paymentStore.RecordReconciliationFailure(ctx, order, key, paymentReconciliationErrorCode(reconcileErr), map[string]any{
					"phase": "provider_query", "payment_provider": order.PaymentProvider,
				}); err != nil {
					return err
				}
				continue
			}
			if _, err = worker.paymentStore.RecordReconciliation(ctx, order, key, result, nil); err != nil {
				return err
			}
		}
		if len(orders) < pageSize {
			return nil
		}
		cursor = orders[len(orders)-1].ID
	}
}

func paymentReconciliationErrorCode(err error) string {
	switch {
	case errors.Is(err, payment.ErrProviderNotRegistered):
		return "provider_not_registered"
	case errors.Is(err, payment.ErrProviderDisabled):
		return "provider_disabled"
	case errors.Is(err, payment.ErrContractUnavailable):
		return "contract_unavailable"
	case errors.Is(err, payment.ErrRegionNotAllowed):
		return "region_not_allowed"
	case errors.Is(err, payment.ErrReconcileUnsupported):
		return "reconciliation_unsupported"
	default:
		value := strings.ToLower(strings.TrimSpace(err.Error()))
		value = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, value)
		value = strings.Trim(value, "_")
		if value == "" || len(value) > 80 {
			return "provider_query_failed"
		}
		return value
	}
}
