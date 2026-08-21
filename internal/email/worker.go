package email

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
	ClaimEmailOutbox(context.Context, string, int, time.Duration) ([]domain.EmailOutbox, error)
	CompleteEmailOutbox(context.Context, string, string) error
	RetryEmailOutbox(context.Context, string, string, string, time.Time) error
	ExpireEmailLeases(context.Context, time.Time) (int64, error)
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
	store    OutboxStore
	vault    SecretVault
	provider Provider
	config   WorkerConfig
	now      func() time.Time
}

func NewWorker(store OutboxStore, vault SecretVault, provider Provider, config WorkerConfig) *Worker {
	if config.WorkerID == "" {
		config.WorkerID = "mail-" + id.UUID()
	}
	if config.BatchSize <= 0 || config.BatchSize > 100 {
		config.BatchSize = 20
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	if config.Lease <= 0 {
		config.Lease = time.Minute
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = 15 * time.Minute
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Worker{store: store, vault: vault, provider: provider, config: config, now: time.Now}
}

func (w *Worker) Run(ctx context.Context) {
	w.processAndLog(ctx)
	ticker := time.NewTicker(w.config.PollInterval)
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
	if _, err := w.store.ExpireEmailLeases(ctx, w.now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
		w.config.Logger.Error("email_lease_expiry_failed", "worker_id", w.config.WorkerID, "error", err)
		return
	}
	count, err := w.ProcessOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		w.config.Logger.Error("email_batch_failed", "worker_id", w.config.WorkerID, "error", err)
	} else if count > 0 {
		w.config.Logger.Debug("email_batch_processed", "worker_id", w.config.WorkerID, "deliveries", count)
	}
}

func (w *Worker) ProcessOnce(ctx context.Context) (int, error) {
	if w.store == nil || w.vault == nil || w.provider == nil {
		return 0, errors.New("email worker dependencies are required")
	}
	items, err := w.store.ClaimEmailOutbox(ctx, w.config.WorkerID, w.config.BatchSize, w.config.Lease)
	if err != nil {
		return 0, err
	}
	var result error
	for _, item := range items {
		plain, processErr := w.vault.Decrypt(item.EncryptedMessage, "email:"+item.ID)
		var message Message
		if processErr == nil {
			processErr = json.Unmarshal([]byte(plain), &message)
		}
		if processErr == nil {
			message.ID = item.ID
			message.To = item.Recipient
			processErr = w.provider.Send(ctx, message)
		}
		if processErr == nil {
			processErr = w.store.CompleteEmailOutbox(ctx, item.ID, item.ClaimToken)
		} else {
			failure := strings.TrimSpace(processErr.Error())
			if len(failure) > 1024 {
				failure = failure[:1024]
			}
			storeErr := w.store.RetryEmailOutbox(ctx, item.ID, item.ClaimToken, failure, w.now().UTC().Add(retryDelay(item.Attempts, w.config.MaxBackoff)))
			processErr = errors.Join(processErr, storeErr)
		}
		result = errors.Join(result, processErr)
	}
	return len(items), result
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
