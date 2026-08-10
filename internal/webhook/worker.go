package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

type OutboxStore interface {
	ClaimWebhookOutbox(context.Context, string, int, time.Duration) ([]domain.WebhookOutbox, error)
	CompleteWebhookOutbox(context.Context, string, string, int, string) error
	RetryWebhookOutbox(context.Context, string, string, int, string, string, time.Time) error
	ExpireWebhookLeases(context.Context, time.Time) (int64, error)
}

type SecretVault interface {
	Decrypt([]byte, string) (string, error)
}

type WorkerConfig struct {
	WorkerID     string
	BatchSize    int
	PollInterval time.Duration
	Lease        time.Duration
	MaxBackoff   time.Duration
	Logger       *slog.Logger
}

type Worker struct {
	store        OutboxStore
	vault        SecretVault
	dispatcher   *Dispatcher
	workerID     string
	batchSize    int
	pollInterval time.Duration
	lease        time.Duration
	maxBackoff   time.Duration
	logger       *slog.Logger
	now          func() time.Time
}

func NewWorker(store OutboxStore, vault SecretVault, dispatcher *Dispatcher, cfg WorkerConfig) *Worker {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "webhook-" + id.UUID()
	}
	if cfg.BatchSize <= 0 || cfg.BatchSize > 100 {
		cfg.BatchSize = 20
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.Lease <= 0 {
		cfg.Lease = time.Minute
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 15 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Worker{
		store: store, vault: vault, dispatcher: dispatcher, workerID: cfg.WorkerID,
		batchSize: cfg.BatchSize, pollInterval: cfg.PollInterval, lease: cfg.Lease,
		maxBackoff: cfg.MaxBackoff, logger: cfg.Logger, now: time.Now,
	}
}

// Run polls immediately, then at the configured interval. Transient database
// errors are logged and retried; cancellation stops new claims promptly.
func (w *Worker) Run(ctx context.Context) {
	w.processAndLog(ctx)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processAndLog(ctx)
		}
	}
}

func (w *Worker) processAndLog(ctx context.Context) {
	if _, err := w.store.ExpireWebhookLeases(ctx, w.now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("webhook_lease_expiry_failed", "worker_id", w.workerID, "error", err)
		return
	}
	processed, err := w.ProcessOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("webhook_batch_failed", "worker_id", w.workerID, "error", err)
		return
	}
	if processed > 0 {
		w.logger.Debug("webhook_batch_processed", "worker_id", w.workerID, "deliveries", processed)
	}
}

func (w *Worker) ProcessOnce(ctx context.Context) (int, error) {
	if w.store == nil || w.vault == nil || w.dispatcher == nil {
		return 0, errors.New("webhook worker dependencies are required")
	}
	deliveries, err := w.store.ClaimWebhookOutbox(ctx, w.workerID, w.batchSize, w.lease)
	if err != nil {
		return 0, err
	}
	var resultErr error
	for _, delivery := range deliveries {
		if err := w.processDelivery(ctx, delivery); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	return len(deliveries), resultErr
}

func (w *Worker) processDelivery(ctx context.Context, item domain.WebhookOutbox) error {
	body, err := json.Marshal(item.Payload)
	if err != nil {
		return w.recordFailure(ctx, item, Result{}, err)
	}
	secret, err := w.vault.Decrypt(item.EncryptedSecret, "webhook:"+item.ProjectID)
	if err != nil {
		return w.recordFailure(ctx, item, Result{}, errors.New("decrypt webhook signing secret"))
	}
	result, deliveryErr := w.dispatcher.Deliver(ctx, Delivery{
		ID: item.ID, EventType: item.EventType, URL: item.EndpointURL, Secret: secret, Body: body,
	})
	if deliveryErr != nil {
		return w.recordFailure(ctx, item, result, deliveryErr)
	}
	if err := w.store.CompleteWebhookOutbox(ctx, item.ID, item.ClaimToken, result.HTTPStatus, result.Response); err != nil {
		return err
	}
	return nil
}

func (w *Worker) recordFailure(ctx context.Context, item domain.WebhookOutbox, result Result, deliveryErr error) error {
	message := "webhook delivery failed"
	if deliveryErr != nil {
		message = strings.TrimSpace(deliveryErr.Error())
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	retryAt := w.now().UTC().Add(retryDelay(item.Attempts, w.maxBackoff))
	if err := w.store.RetryWebhookOutbox(ctx, item.ID, item.ClaimToken, result.HTTPStatus, result.Response, message, retryAt); err != nil {
		return err
	}
	return nil
}

func retryDelay(attempt int, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 20 {
		shift = 20
	}
	delay := time.Second * time.Duration(1<<shift)
	if maximum > 0 && delay > maximum {
		return maximum
	}
	return delay
}
