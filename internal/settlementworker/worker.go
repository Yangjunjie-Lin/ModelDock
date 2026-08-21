package settlementworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/payout"
	"github.com/relayedock/relayedock/internal/store"
)

// settlementStore mirrors CompleteSupplierPayout with its concrete return type
// while keeping the worker easy to exercise with a fake store.
type settlementStore interface {
	AccrueEligibleSupplierUsage(context.Context, int) (int, error)
	ReleaseMatureSupplierReserves(context.Context, time.Time, int) (int, error)
	RunDueSupplierSettlementCycles(context.Context, time.Time, int) (int, error)
	ClaimSupplierPayout(context.Context, time.Time) (store.SupplierPayoutJob, error)
	FailSupplierPayout(context.Context, string, string, string, time.Time) error
	CompleteSupplierPayout(context.Context, string, string, string, map[string]any, time.Time) (domain.SupplierSettlementBatch, bool, error)
}

type Worker struct {
	store        settlementStore
	vault        *secretcrypto.Vault
	registry     *payout.Registry
	pollInterval time.Duration
	batchSize    int
	logger       *slog.Logger
}

func NewWorker(store settlementStore, vault *secretcrypto.Vault, registry *payout.Registry, pollInterval time.Duration, batchSize int, logger *slog.Logger) *Worker {
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}
	if batchSize <= 0 || batchSize > 500 {
		batchSize = 100
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, vault: vault, registry: registry, pollInterval: pollInterval, batchSize: batchSize, logger: logger}
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.store == nil || worker.vault == nil || worker.registry == nil {
		return
	}
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	worker.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.runOnce(ctx)
		}
	}
}

func (worker *Worker) runOnce(ctx context.Context) {
	now := time.Now().UTC()
	if _, err := worker.store.AccrueEligibleSupplierUsage(ctx, worker.batchSize); err != nil {
		worker.logger.Error("supplier_payable_accrual_failed", "error", err)
		return
	}
	if _, err := worker.store.ReleaseMatureSupplierReserves(ctx, now, worker.batchSize); err != nil {
		worker.logger.Error("supplier_reserve_release_failed", "error", err)
		return
	}
	if _, err := worker.store.RunDueSupplierSettlementCycles(ctx, now, worker.batchSize); err != nil {
		worker.logger.Error("supplier_settlement_cycle_failed", "error", err)
		return
	}
	for i := 0; i < worker.batchSize; i++ {
		job, err := worker.store.ClaimSupplierPayout(ctx, now)
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			worker.logger.Error("supplier_payout_claim_failed", "error", err)
			return
		}
		destination, err := worker.vault.Decrypt(job.PayoutAccount, "supplier-payout:"+job.PayoutAccountOwner)
		if err != nil {
			_ = worker.store.FailSupplierPayout(ctx, job.Batch.ID, job.AttemptID, "payout_destination_decrypt_failed", time.Now().UTC())
			continue
		}
		adapter, err := worker.registry.Resolve(job.Batch.PayoutAdapter, job.Batch.PayoutRegion)
		if err != nil {
			_ = worker.store.FailSupplierPayout(ctx, job.Batch.ID, job.AttemptID, "payout_adapter_unavailable", time.Now().UTC())
			continue
		}
		result, err := adapter.Send(ctx, payout.Request{SettlementID: job.Batch.ID, IdempotencyKey: "supplier-payout:" + job.Batch.ID,
			Amount: job.Batch.PayoutAmount, Currency: job.Batch.Currency, Region: job.Batch.PayoutRegion, Destination: destination})
		destination = ""
		if err != nil || result.Status != "SUCCEEDED" {
			_ = worker.store.FailSupplierPayout(ctx, job.Batch.ID, job.AttemptID, "payout_send_failed", time.Now().UTC())
			continue
		}
		if _, _, err = worker.store.CompleteSupplierPayout(ctx, job.Batch.ID, job.AttemptID, result.ProviderReference, result.Metadata, time.Now().UTC()); err != nil {
			worker.logger.Error("supplier_payout_completion_failed", "settlement_id", job.Batch.ID, "error", err)
		}
	}
}
