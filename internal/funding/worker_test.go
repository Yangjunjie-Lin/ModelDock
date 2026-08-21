package funding

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type recoveryStore struct {
	mu          sync.Mutex
	calls       int
	staleBefore time.Time
}

func (store *recoveryStore) RecoverStaleFunding(_ context.Context, before time.Time, _ int) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.calls++
	store.staleBefore = before
	return 1, nil
}

func TestWorkerRunsRecoveryAndStops(t *testing.T) {
	store := &recoveryStore{}
	worker := NewWorker(store, 5*time.Millisecond, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		calls := store.calls
		store.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.calls == 0 {
		t.Fatal("worker did not run recovery")
	}
	if store.staleBefore.After(time.Now().Add(-50 * time.Second)) {
		t.Fatalf("stale cutoff=%s", store.staleBefore)
	}
}
