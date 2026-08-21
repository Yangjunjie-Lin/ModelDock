// Package subscription runs idempotent subscription lifecycle transitions.
// PostgreSQL row locks and event keys make multiple replicas safe.
package subscription

import (
	"context"
	"log/slog"
	"time"
)

type LifecycleStore interface {
	ProcessSubscriptionLifecycle(context.Context, time.Time, int) (int, error)
}

type Worker struct {
	store        LifecycleStore
	pollInterval time.Duration
	logger       *slog.Logger
}

func NewWorker(store LifecycleStore, pollInterval time.Duration, logger *slog.Logger) *Worker {
	if pollInterval <= 0 {
		pollInterval = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, pollInterval: pollInterval, logger: logger}
}

func (worker *Worker) Run(ctx context.Context) {
	worker.process(ctx)
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.process(ctx)
		}
	}
}

func (worker *Worker) process(ctx context.Context) {
	count, err := worker.store.ProcessSubscriptionLifecycle(ctx, time.Now().UTC(), 100)
	if err != nil {
		worker.logger.Error("subscription_lifecycle_failed", "error", err)
		return
	}
	if count > 0 {
		worker.logger.Info("subscription_lifecycle_processed", "count", count)
	}
}
