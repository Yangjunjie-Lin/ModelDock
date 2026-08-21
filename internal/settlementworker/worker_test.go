package settlementworker

import (
	"context"
	"errors"
	"testing"
	"time"

	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/payout"
	"github.com/relayedock/relayedock/internal/store"
)

type workerTestStore struct {
	job       store.SupplierPayoutJob
	claimed   bool
	failed    int
	completed int
}

func (value *workerTestStore) AccrueEligibleSupplierUsage(context.Context, int) (int, error) {
	return 1, nil
}
func (value *workerTestStore) ReleaseMatureSupplierReserves(context.Context, time.Time, int) (int, error) {
	return 1, nil
}
func (value *workerTestStore) RunDueSupplierSettlementCycles(context.Context, time.Time, int) (int, error) {
	return 1, nil
}
func (value *workerTestStore) ClaimSupplierPayout(context.Context, time.Time) (store.SupplierPayoutJob, error) {
	if value.claimed {
		return store.SupplierPayoutJob{}, store.ErrNotFound
	}
	value.claimed = true
	return value.job, nil
}
func (value *workerTestStore) FailSupplierPayout(context.Context, string, string, string, time.Time) error {
	value.failed++
	return nil
}
func (value *workerTestStore) CompleteSupplierPayout(context.Context, string, string, string, map[string]any, time.Time) (domain.SupplierSettlementBatch, bool, error) {
	value.completed++
	return value.job.Batch, false, nil
}

type workerTestAdapter struct{ fail bool }

func (adapter workerTestAdapter) Capabilities() payout.Capabilities {
	return payout.Capabilities{Name: "worker-test", Enabled: true, ContractStatus: "TEST_ONLY", AllowedRegions: []string{"US"}}
}
func (adapter workerTestAdapter) Send(_ context.Context, request payout.Request) (payout.Result, error) {
	if request.Destination != "synthetic-destination" {
		return payout.Result{}, errors.New("destination was not decrypted")
	}
	if adapter.fail {
		return payout.Result{}, errors.New("synthetic adapter failure")
	}
	return payout.Result{ProviderReference: "synthetic-reference", Status: "SUCCEEDED", Metadata: map[string]any{"synthetic": true}}, nil
}

func TestWorkerCompletesOnlyAdapterConfirmedPayout(t *testing.T) {
	vault, err := secretcrypto.NewVault([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.Encrypt("synthetic-destination", "supplier-payout:owner")
	if err != nil {
		t.Fatal(err)
	}
	fake := &workerTestStore{job: store.SupplierPayoutJob{AttemptID: "attempt", PayoutAccount: envelope, PayoutAccountOwner: "owner",
		Batch: domain.SupplierSettlementBatch{ID: "batch", PayoutAdapter: "worker-test", PayoutRegion: "US", PayoutAmount: domain.Decimal("12.34"), Currency: "USD"}}}
	registry := payout.NewRegistry()
	registry.Register(workerTestAdapter{})
	worker := NewWorker(fake, vault, registry, time.Minute, 10, nil)
	worker.runOnce(context.Background())
	if fake.completed != 1 || fake.failed != 0 {
		t.Fatalf("completed=%d failed=%d", fake.completed, fake.failed)
	}
}

func TestWorkerRecordsRetryableAdapterFailure(t *testing.T) {
	vault, _ := secretcrypto.NewVault([]byte("0123456789abcdef0123456789abcdef"))
	envelope, _ := vault.Encrypt("synthetic-destination", "supplier-payout:owner")
	fake := &workerTestStore{job: store.SupplierPayoutJob{AttemptID: "attempt", PayoutAccount: envelope, PayoutAccountOwner: "owner",
		Batch: domain.SupplierSettlementBatch{ID: "batch", PayoutAdapter: "worker-test", PayoutRegion: "US", PayoutAmount: domain.Decimal("1"), Currency: "USD"}}}
	registry := payout.NewRegistry()
	registry.Register(workerTestAdapter{fail: true})
	NewWorker(fake, vault, registry, time.Minute, 10, nil).runOnce(context.Background())
	if fake.failed != 1 || fake.completed != 0 {
		t.Fatalf("failed=%d completed=%d", fake.failed, fake.completed)
	}
}
