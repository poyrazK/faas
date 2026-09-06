package githubd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type checkUpdateStoreStub struct {
	mu       sync.Mutex
	update   CheckUpdate
	claimed  bool
	complete bool
	failed   bool
	dead     bool
	cancel   context.CancelFunc
}

func (s *checkUpdateStoreStub) Claim(context.Context) (CheckUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed {
		return CheckUpdate{}, ErrNoCheckUpdate
	}
	s.claimed = true
	return s.update, nil
}

func (s *checkUpdateStoreStub) Complete(_ context.Context, _ CheckUpdate) error {
	s.mu.Lock()
	s.complete = true
	s.mu.Unlock()
	s.cancel()
	return nil
}

func (s *checkUpdateStoreStub) Fail(_ context.Context, _ CheckUpdate, _ string, _ time.Time, dead bool) error {
	s.mu.Lock()
	s.failed, s.dead = true, dead
	s.mu.Unlock()
	s.cancel()
	return nil
}

func TestRunCheckUpdateWorkerCompletesSuccessfulProjection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &checkUpdateStoreStub{
		update: CheckUpdate{DeploymentID: "dep-1", Generation: 4, Attempts: 1},
		cancel: cancel,
	}
	RunCheckUpdateWorker(ctx, store, func(_ context.Context, id string) error {
		if id != "dep-1" {
			t.Fatalf("deployment id = %q", id)
		}
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !store.complete || store.failed {
		t.Fatalf("complete=%v failed=%v", store.complete, store.failed)
	}
}

func TestRunCheckUpdateWorkerDeadLettersExhaustedProjection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &checkUpdateStoreStub{
		update: CheckUpdate{DeploymentID: "dep-2", Generation: 7, Attempts: checkUpdateMaxAttempts},
		cancel: cancel,
	}
	RunCheckUpdateWorker(ctx, store, func(context.Context, string) error {
		return errors.New("github unavailable")
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !store.failed || !store.dead || store.complete {
		t.Fatalf("failed=%v dead=%v complete=%v", store.failed, store.dead, store.complete)
	}
}
