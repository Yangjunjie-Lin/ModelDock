package governance

import (
	"context"
	"log/slog"
	"time"
)

type Store interface {
	ProcessNextLifecycleJob(context.Context) (bool, error)
	CleanupExpiredGovernanceData(context.Context) (int64, error)
}
type Worker struct {
	store    Store
	interval time.Duration
	logger   *slog.Logger
}

func NewWorker(store Store, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Worker{store: store, interval: interval, logger: logger}
}
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		w.run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *Worker) run(ctx context.Context) {
	for i := 0; i < 100; i++ {
		processed, err := w.store.ProcessNextLifecycleJob(ctx)
		if err != nil {
			w.logger.Error("governance_lifecycle_failed", "error", err)
			break
		}
		if !processed {
			break
		}
	}
	if count, err := w.store.CleanupExpiredGovernanceData(ctx); err != nil {
		w.logger.Error("governance_cleanup_failed", "error", err)
	} else if count > 0 {
		w.logger.Info("governance_cleanup_completed", "rows_redacted", count)
	}
}
