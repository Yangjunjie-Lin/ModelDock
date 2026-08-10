package backend_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/scheduler"
)

type fakeSource struct{ candidates []domain.Credential }

func (f fakeSource) Candidates(context.Context, string) ([]domain.Credential, error) {
	return append([]domain.Credential(nil), f.candidates...), nil
}

type fakeCounter struct {
	mu     sync.Mutex
	active map[string]int64
	fail   bool
}

func (f *fakeCounter) Active(_ context.Context, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return 0, errors.New("redis down")
	}
	return f.active[id], nil
}
func (f *fakeCounter) TryAcquire(_ context.Context, id string, max int) (bool, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return false, 0, errors.New("redis down")
	}
	if f.active[id] >= int64(max) {
		return false, f.active[id], nil
	}
	f.active[id]++
	return true, f.active[id], nil
}
func (f *fakeCounter) Release(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active[id] > 0 {
		f.active[id]--
	}
	return nil
}

func TestSchedulerIsDeterministicExplainableAndCapacitySafe(t *testing.T) {
	source := fakeSource{candidates: []domain.Credential{
		{ID: "low-priority", Status: "ACTIVE", CurrentHealth: "HEALTHY", EffectivePriority: 1, EffectiveWeight: 100, MaxConcurrency: 10},
		{ID: "preferred-busy", Status: "ACTIVE", CurrentHealth: "HEALTHY", EffectivePriority: 10, EffectiveWeight: 100, MaxConcurrency: 10},
		{ID: "preferred-idle", Status: "ACTIVE", CurrentHealth: "HEALTHY", EffectivePriority: 10, EffectiveWeight: 60, MaxConcurrency: 1},
	}}
	counter := &fakeCounter{active: map[string]int64{"preferred-busy": 2}}
	s := scheduler.New(source, counter)
	selection, err := s.Select(context.Background(), "group")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Credential.ID != "preferred-idle" {
		t.Fatalf("selected %s", selection.Credential.ID)
	}
	if selection.Reason["policy"] != "priority_weighted" {
		t.Fatalf("missing explanation: %#v", selection.Reason)
	}
	selection.Release()
	selection.Release()
	if counter.active["preferred-idle"] != 0 {
		t.Fatal("release must be idempotent and non-negative")
	}
	selection, err = s.Select(context.Background(), "group")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Credential.ID != "preferred-idle" {
		t.Fatal("selection is not deterministic")
	}
	selection.Release()
}

func TestSchedulerLeastLoadedPolicyPrefersCapacity(t *testing.T) {
	source := fakeSource{candidates: []domain.Credential{
		{ID: "high-priority-busy", EffectivePriority: 100, MaxConcurrency: 10, Weight: 100},
		{ID: "low-priority-idle", EffectivePriority: 1, MaxConcurrency: 10, Weight: 1},
	}}
	counter := &fakeCounter{active: map[string]int64{"high-priority-busy": 8}}
	selection, err := scheduler.New(source, counter).Select(context.Background(), "group", "least_loaded")
	if err != nil {
		t.Fatal(err)
	}
	defer selection.Release()
	if selection.Credential.ID != "low-priority-idle" {
		t.Fatalf("selected %s", selection.Credential.ID)
	}
	if selection.Reason["policy"] != "least_loaded" {
		t.Fatalf("unexpected policy: %#v", selection.Reason)
	}
}

func TestSchedulerFailsClosedWhenCounterUnavailable(t *testing.T) {
	s := scheduler.New(fakeSource{candidates: []domain.Credential{{ID: "a", MaxConcurrency: 1, Weight: 1}}}, &fakeCounter{active: map[string]int64{}, fail: true})
	_, err := s.Select(context.Background(), "group")
	if !errors.Is(err, scheduler.ErrStateUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestSchedulerSkipsCredentialsAtCapacity(t *testing.T) {
	source := fakeSource{candidates: []domain.Credential{{ID: "full", EffectivePriority: 20, MaxConcurrency: 1, Weight: 100}, {ID: "free", EffectivePriority: 1, MaxConcurrency: 2, Weight: 1}}}
	counter := &fakeCounter{active: map[string]int64{"full": 1}}
	selection, err := scheduler.New(source, counter).Select(context.Background(), "group")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Credential.ID != "free" {
		t.Fatalf("selected %s", selection.Credential.ID)
	}
	selection.Release()
}

func TestSchedulerEnforcesRequiredAndExcludedCredentialTags(t *testing.T) {
	source := fakeSource{candidates: []domain.Credential{
		{ID: "wrong-region", Tags: []string{"region:us", "policy:production"}, EffectivePriority: 100, MaxConcurrency: 2, Weight: 100},
		{ID: "maintenance", Tags: []string{"region:apac", "policy:production", "maintenance"}, EffectivePriority: 50, MaxConcurrency: 2, Weight: 100},
		{ID: "eligible", Tags: []string{"region:apac", "policy:production"}, EffectivePriority: 1, MaxConcurrency: 2, Weight: 1},
	}}
	counter := &fakeCounter{active: map[string]int64{}}
	selection, err := scheduler.New(source, counter).SelectConstrained(context.Background(), "group", "priority_weighted", scheduler.CredentialConstraints{
		RequiredTags: []string{"region:apac", "policy:production"},
		ExcludedTags: []string{"maintenance"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer selection.Release()
	if selection.Credential.ID != "eligible" {
		t.Fatalf("selected %s", selection.Credential.ID)
	}
	if got := selection.Reason["required_credential_tags"].([]string); len(got) != 2 {
		t.Fatalf("missing tag explanation: %#v", selection.Reason)
	}
}

func TestCredentialHTTPTransitions(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	auth := scheduler.TransitionForHTTP(401, now, 30*time.Second)
	if auth.Status != "AUTH_FAILED" || auth.CooldownUntil != nil {
		t.Fatalf("bad 401 transition: %#v", auth)
	}
	limited := scheduler.TransitionForHTTP(429, now, 30*time.Second)
	if limited.Status != "COOLDOWN" || limited.CooldownUntil == nil || !limited.CooldownUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("bad 429 transition: %#v", limited)
	}
	clientError := scheduler.TransitionForHTTP(400, now, 30*time.Second)
	if clientError.Status != "" {
		t.Fatalf("client error poisoned credential: %#v", clientError)
	}
	success := scheduler.TransitionForHTTP(200, now, 30*time.Second)
	if !success.MarkSuccess {
		t.Fatal("success was not recorded")
	}
}
