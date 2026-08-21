// Package funding recovers reservations left by a process that stopped before
// committing a deterministic settlement. PostgreSQL row locks and terminal
// state checks make every worker replica safe to run concurrently.
package funding

import (
	"context"
	"log/slog"
	"time"
)

type RecoveryStore interface {
	RecoverStaleFunding(context.Context, time.Time, int) (int, error)
}

type Worker struct {
	store        RecoveryStore
	pollInterval time.Duration
	staleAfter   time.Duration
	logger       *slog.Logger
}

func NewWorker(store RecoveryStore, pollInterval, staleAfter time.Duration, logger *slog.Logger) *Worker {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, pollInterval: pollInterval, staleAfter: staleAfter, logger: logger}
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.store == nil {
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
	recovered, err := worker.store.RecoverStaleFunding(ctx, time.Now().UTC().Add(-worker.staleAfter), 100)
	if err != nil {
		worker.logger.Error("funding_recovery_failed", "error", err)
		return
	}
	if recovered > 0 {
		worker.logger.Info("funding_reservations_recovered", "count", recovered)
	}
}
