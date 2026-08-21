package governance

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type workerStore struct{ processed, cleanup int }

func (s *workerStore) ProcessNextLifecycleJob(context.Context) (bool, error) {
	s.processed++
	return s.processed == 1, nil
}
func (s *workerStore) CleanupExpiredGovernanceData(context.Context) (int64, error) {
	s.cleanup++
	return 2, nil
}

func TestWorkerProcessesQueueAndCleanup(t *testing.T) {
	s := &workerStore{}
	w := NewWorker(s, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.run(context.Background())
	if s.processed != 2 || s.cleanup != 1 {
		t.Fatalf("processed=%d cleanup=%d", s.processed, s.cleanup)
	}
}
