package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
)

type lifecycleStoreStub struct {
	count int
	err   error
	calls int
}

func (store *lifecycleStoreStub) ProcessSubscriptionLifecycle(context.Context, time.Time, int) (int, error) {
	store.calls++
	return store.count, store.err
}

func TestWorkerProcessInvokesLifecycle(t *testing.T) {
	store := &lifecycleStoreStub{count: 2}
	worker := NewWorker(store, time.Minute, nil)
	worker.process(context.Background())
	if store.calls != 1 {
		t.Fatalf("calls=%d want 1", store.calls)
	}
}

func TestWorkerProcessHandlesStoreError(t *testing.T) {
	store := &lifecycleStoreStub{err: errors.New("synthetic lifecycle failure")}
	worker := NewWorker(store, time.Minute, nil)
	worker.process(context.Background())
	if store.calls != 1 {
		t.Fatalf("calls=%d want 1", store.calls)
	}
}
