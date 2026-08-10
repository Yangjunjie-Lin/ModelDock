package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

type workerStoreStub struct {
	claimed   []domain.WebhookOutbox
	completed []string
	retried   []string
	status    int
	retryAt   time.Time
}

func (s *workerStoreStub) ClaimWebhookOutbox(context.Context, string, int, time.Duration) ([]domain.WebhookOutbox, error) {
	return s.claimed, nil
}

func (s *workerStoreStub) CompleteWebhookOutbox(_ context.Context, id, _ string, status int, _ string) error {
	s.completed = append(s.completed, id)
	s.status = status
	return nil
}

func (s *workerStoreStub) RetryWebhookOutbox(_ context.Context, id, _ string, status int, _, _ string, retryAt time.Time) error {
	s.retried = append(s.retried, id)
	s.status = status
	s.retryAt = retryAt
	return nil
}

func (s *workerStoreStub) ExpireWebhookLeases(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type workerVaultStub struct {
	secret string
	aad    string
}

func (v *workerVaultStub) Decrypt(_ []byte, aad string) (string, error) {
	v.aad = aad
	return v.secret, nil
}

func TestWorkerCompletesSignedDeliveryWithOutboxIdentity(t *testing.T) {
	receivedDelivery := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedDelivery = r.Header.Get("X-RelayDock-Delivery")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	store := &workerStoreStub{claimed: []domain.WebhookOutbox{{
		ID: "outbox-1", EventID: "event-1", EventType: "webhook.test", ProjectID: "project-1",
		EndpointURL: server.URL, EncryptedSecret: []byte("encrypted"), ClaimToken: "claim-1",
		Payload: map[string]any{"id": "event-1", "type": "webhook.test"}, Attempts: 1,
	}}}
	vault := &workerVaultStub{secret: "0123456789abcdef"}
	worker := NewWorker(store, vault, New(Config{AllowHTTP: true, AllowPrivateNetwork: true}), WorkerConfig{})

	processed, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if processed != 1 || len(store.completed) != 1 || store.completed[0] != "outbox-1" {
		t.Fatalf("unexpected completion: processed=%d completed=%v", processed, store.completed)
	}
	if receivedDelivery != "outbox-1" {
		t.Fatalf("delivery header = %q, want outbox identity", receivedDelivery)
	}
	if vault.aad != "webhook:project-1" {
		t.Fatalf("vault AAD = %q", vault.aad)
	}
}

func TestWorkerRecordsBoundedExponentialRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "retry", http.StatusInternalServerError)
	}))
	defer server.Close()

	store := &workerStoreStub{claimed: []domain.WebhookOutbox{{
		ID: "outbox-2", EventID: "event-2", EventType: "webhook.test", ProjectID: "project-2",
		EndpointURL: server.URL, EncryptedSecret: []byte("encrypted"), ClaimToken: "claim-2",
		Payload: map[string]any{"id": "event-2"}, Attempts: 3, MaxAttempts: 6,
	}}}
	worker := NewWorker(store, &workerVaultStub{secret: "0123456789abcdef"}, New(Config{AllowHTTP: true, AllowPrivateNetwork: true}), WorkerConfig{MaxBackoff: 3 * time.Second})
	now := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }

	processed, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if processed != 1 || len(store.retried) != 1 || store.retried[0] != "outbox-2" {
		t.Fatalf("unexpected retry: processed=%d retried=%v", processed, store.retried)
	}
	if store.status != http.StatusInternalServerError {
		t.Fatalf("retry status = %d", store.status)
	}
	if want := now.Add(3 * time.Second); !store.retryAt.Equal(want) {
		t.Fatalf("retry at %s, want %s", store.retryAt, want)
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	if got := retryDelay(1, 15*time.Minute); got != time.Second {
		t.Fatalf("first retry delay = %s", got)
	}
	if got := retryDelay(50, 15*time.Minute); got != 15*time.Minute {
		t.Fatalf("bounded retry delay = %s", got)
	}
}
