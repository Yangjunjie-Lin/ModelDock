package email

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
)

type workerStore struct {
	items       []domain.EmailOutbox
	completedID string
	retriedID   string
	retryAt     time.Time
}

func (s *workerStore) ClaimEmailOutbox(context.Context, string, int, time.Duration) ([]domain.EmailOutbox, error) {
	return s.items, nil
}
func (s *workerStore) CompleteEmailOutbox(_ context.Context, id, _ string) error {
	s.completedID = id
	return nil
}
func (s *workerStore) RetryEmailOutbox(_ context.Context, id, _ string, _ string, at time.Time) error {
	s.retriedID, s.retryAt = id, at
	return nil
}
func (*workerStore) ExpireEmailLeases(context.Context, time.Time) (int64, error) { return 0, nil }

type workerVault struct{ plaintext string }

func (v workerVault) Decrypt([]byte, string) (string, error) { return v.plaintext, nil }

type workerProvider struct {
	message Message
	err     error
}

func (p *workerProvider) Send(_ context.Context, message Message) error {
	p.message = message
	return p.err
}

func TestWorkerCompletesDeliveredOutboxItem(t *testing.T) {
	store := &workerStore{items: []domain.EmailOutbox{{ID: "message-1", Recipient: "user@example.invalid", EncryptedMessage: []byte("ciphertext"), ClaimToken: "claim-1", Attempts: 1}}}
	provider := &workerProvider{}
	worker := NewWorker(store, workerVault{plaintext: `{"from":"ModelDock <no-reply@example.invalid>","to":"user@example.invalid","subject":"Verify","text":"synthetic body"}`}, provider, WorkerConfig{})
	count, err := worker.ProcessOnce(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("ProcessOnce() count=%d error=%v", count, err)
	}
	if store.completedID != "message-1" || store.retriedID != "" {
		t.Fatalf("unexpected store result: complete=%q retry=%q", store.completedID, store.retriedID)
	}
	if provider.message.ID != "message-1" || provider.message.To != "user@example.invalid" {
		t.Fatalf("provider message was not bound to claimed outbox metadata: %#v", provider.message)
	}
}

func TestWorkerSchedulesRetryAfterProviderFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	store := &workerStore{items: []domain.EmailOutbox{{ID: "message-2", Recipient: "user@example.invalid", EncryptedMessage: []byte("ciphertext"), ClaimToken: "claim-2", Attempts: 3}}}
	provider := &workerProvider{err: errors.New("synthetic SMTP failure")}
	worker := NewWorker(store, workerVault{plaintext: `{"from":"ModelDock <no-reply@example.invalid>","to":"user@example.invalid","subject":"Reset","text":"synthetic body"}`}, provider, WorkerConfig{MaxBackoff: time.Minute})
	worker.now = func() time.Time { return now }
	count, err := worker.ProcessOnce(context.Background())
	if err == nil || count != 1 {
		t.Fatalf("ProcessOnce() count=%d error=%v, want provider error", count, err)
	}
	if store.retriedID != "message-2" || !store.retryAt.Equal(now.Add(4*time.Second)) {
		t.Fatalf("retry id=%q at=%s", store.retriedID, store.retryAt)
	}
	if store.completedID != "" {
		t.Fatalf("failed delivery was completed: %q", store.completedID)
	}
}
