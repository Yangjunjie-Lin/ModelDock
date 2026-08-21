package reconciliation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/payment"
)

type captureStore struct{ dates []time.Time }

func (store *captureStore) RunFinancialReconciliation(_ context.Context, date time.Time, _ string, _ *string) (domain.ReconciliationRun, bool, error) {
	store.dates = append(store.dates, date)
	return domain.ReconciliationRun{ID: "run", BusinessDate: date.Format("2006-01-02"), Summary: map[string]any{}}, false, nil
}

func TestWorkerUsesPreviousUTCBusinessDateAfterRunAt(t *testing.T) {
	store := &captureStore{}
	worker := NewWorker(store, time.Hour, 2*time.Hour, nil)
	worker.reconcile(context.Background(), time.Date(2026, 8, 12, 2, 1, 0, 0, time.UTC))
	if len(store.dates) != 1 || store.dates[0].Format("2006-01-02") != "2026-08-11" {
		t.Fatalf("dates = %#v", store.dates)
	}
}

func TestWorkerWaitsUntilRunAt(t *testing.T) {
	store := &captureStore{}
	worker := NewWorker(store, time.Hour, 2*time.Hour, nil)
	worker.reconcile(context.Background(), time.Date(2026, 8, 12, 1, 59, 0, 0, time.UTC))
	if len(store.dates) != 0 {
		t.Fatalf("unexpected reconciliation: %#v", store.dates)
	}
}

func TestWorkerRunNormalizesInjectedClockToUTC(t *testing.T) {
	store := &captureStore{}
	worker := NewWorker(store, time.Hour, 2*time.Hour, nil)
	worker.now = func() time.Time {
		return time.Date(2026, 8, 12, 10, 1, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.Run(ctx)
	if len(store.dates) != 1 || store.dates[0].Format("2006-01-02") != "2026-08-11" {
		t.Fatalf("dates = %#v", store.dates)
	}
}

type paymentCaptureStore struct {
	captureStore
	orders   []domain.RechargeOrder
	records  []string
	failures []string
}

func (store *paymentCaptureStore) ListRechargeOrdersForReconciliation(_ context.Context, _ time.Time, afterID string, _ int) ([]domain.RechargeOrder, error) {
	if afterID != "" {
		return nil, nil
	}
	return append([]domain.RechargeOrder(nil), store.orders...), nil
}

func (store *paymentCaptureStore) RecordReconciliation(_ context.Context, order domain.RechargeOrder, key string, _ payment.ReconcileResult, _ *string) (domain.PaymentReconciliationRecord, error) {
	store.records = append(store.records, order.ID+":"+key)
	return domain.PaymentReconciliationRecord{ID: "record"}, nil
}

func (store *paymentCaptureStore) RecordReconciliationFailure(_ context.Context, order domain.RechargeOrder, _ string, code string, _ map[string]any) (domain.PaymentReconciliationRecord, error) {
	store.failures = append(store.failures, order.ID+":"+code)
	return domain.PaymentReconciliationRecord{ID: "failure"}, nil
}

type reconcileProvider struct {
	name      string
	supported bool
	result    payment.ReconcileResult
	err       error
	callCount int
}

func (provider *reconcileProvider) Capabilities() payment.Capabilities {
	return payment.Capabilities{Name: provider.name, Enabled: true, ContractStatus: "TEST_ONLY", AllowedRegions: []string{"US"}, SupportsReconcile: provider.supported}
}
func (*reconcileProvider) CreatePayment(context.Context, payment.CreateRequest) (payment.PaymentResult, error) {
	return payment.PaymentResult{}, nil
}
func (*reconcileProvider) QueryPayment(context.Context, payment.QueryRequest) (payment.PaymentResult, error) {
	return payment.PaymentResult{}, nil
}
func (*reconcileProvider) VerifyWebhook(context.Context, payment.WebhookRequest) (payment.VerifiedWebhook, error) {
	return payment.VerifiedWebhook{}, payment.ErrWebhookUnsupported
}
func (*reconcileProvider) RefundPayment(context.Context, payment.RefundRequest) (payment.RefundResult, error) {
	return payment.RefundResult{}, nil
}
func (*reconcileProvider) ClosePayment(context.Context, payment.QueryRequest) (payment.PaymentResult, error) {
	return payment.PaymentResult{}, nil
}
func (provider *reconcileProvider) ReconcilePayment(context.Context, payment.ReconcileRequest) (payment.ReconcileResult, error) {
	provider.callCount++
	return provider.result, provider.err
}

func TestWorkerPersistsPaymentChannelEvidenceAndFailures(t *testing.T) {
	store := &paymentCaptureStore{orders: []domain.RechargeOrder{
		{ID: "00000000-0000-4000-8000-000000000001", PaymentProvider: "matched", Region: "US", Status: "PAID", Amount: domain.Decimal("1"), Currency: "USD"},
		{ID: "00000000-0000-4000-8000-000000000002", PaymentProvider: "unsupported", Region: "US", Status: "PAID", Amount: domain.Decimal("1"), Currency: "USD"},
		{ID: "00000000-0000-4000-8000-000000000003", PaymentProvider: "errored", Region: "US", Status: "PAID", Amount: domain.Decimal("1"), Currency: "USD"},
	}}
	registry := payment.NewRegistry()
	matched := &reconcileProvider{name: "matched", supported: true, result: payment.ReconcileResult{ProviderStatus: "PAID", Amount: domain.Decimal("1"), Currency: "USD", EvidenceSource: "PROVIDER_API"}}
	registry.Register(matched)
	registry.Register(&reconcileProvider{name: "unsupported", supported: false})
	registry.Register(&reconcileProvider{name: "errored", supported: true, err: errors.New("query unavailable")})
	worker := NewWorkerWithPayments(store, registry, time.Hour, 2*time.Hour, nil)
	worker.reconcile(context.Background(), time.Date(2026, 8, 12, 2, 1, 0, 0, time.UTC))
	if matched.callCount != 1 || len(store.records) != 1 || len(store.failures) != 2 || len(store.dates) != 1 {
		t.Fatalf("calls=%d records=%v failures=%v runs=%v", matched.callCount, store.records, store.failures, store.dates)
	}
	if store.records[0] != "00000000-0000-4000-8000-000000000001:daily:2026-08-11:00000000-0000-4000-8000-000000000001" {
		t.Fatalf("non-deterministic reconciliation key: %s", store.records[0])
	}
}
