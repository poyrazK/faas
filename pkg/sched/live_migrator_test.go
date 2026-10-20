// live_migrator_test.go — table-driven tests for Tier A5's
// LiveMigrator watcher (ADR-066). Parallels rebalancer_test.go
// shape:
//
//   - active=false JSON payload → handle called once with the
//     dead_node_id.
//   - active=true payload → handle NOT called.
//   - Literal "compute_node_keys" payload → handle NOT called.
//   - Malformed JSON → handle NOT called, loop survives.
//   - handle returns err → watcher logs + continues; next
//     payload still honoured.
//   - Back-to-back payloads for the same dead node → handle
//     called twice.
//   - Ctx cancel pre-notify → watcher returns within deadline.
//
// The engine-side policy (per-instance four-phase handoff +
// per-tick cap + metric) is exercised separately by
// pkg/sched/migration_handoff_test.go — this file is the
// "watcher does the right filtering and dispatch" half of the
// contract, mirrored from rebalancer_test.go.

package sched_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
)

// recordingLiveMigratorHandle is the test stand-in for
// Engine.MigrateLiveInstances. Records every (deadNodeID,
// attempted) pair and supports the same errAfter injection as
// recordingRebalancerHandle.
type recordingLiveMigratorHandle struct {
	mu       sync.Mutex
	seen     []string
	attempts []int
	calls    atomic.Int32
	errAfter *atomic.Int32
	err      error
}

func (r *recordingLiveMigratorHandle) fn(ctx context.Context, deadNodeID string) (int, error) {
	r.calls.Add(1)
	r.mu.Lock()
	n := int32(0)
	if r.errAfter != nil {
		n = r.errAfter.Load()
	}
	r.mu.Unlock()
	if r.errAfter != nil && n > 0 {
		r.errAfter.Add(-1)
		return 0, r.err
	}
	r.mu.Lock()
	r.seen = append(r.seen, deadNodeID)
	r.attempts = append(r.attempts, 3) // arbitrary > 0
	r.mu.Unlock()
	return 3, nil
}

func TestLiveMigrator_DispatchesOnDrain(t *testing.T) {
	rec := &recordingLiveMigratorHandle{}
	m := sched.NewLiveMigrator(rec.fn, nil)
	notif := make(chan db.Notification, 1)
	notif <- db.Notification{Payload: `{"node_id":"dead-1","active":false}`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, notif) }()
	if err := sched.WaitFor(func() bool {
		return rec.calls.Load() == 1
	}, 200*time.Millisecond); err != nil {
		t.Fatalf("handle not called within 200ms: %v", err)
	}
	close(notif)
	cancel()
	<-done
	if len(rec.seen) != 1 || rec.seen[0] != "dead-1" {
		t.Fatalf("seen=%v want [dead-1]", rec.seen)
	}
}

func TestLiveMigrator_FilterActive(t *testing.T) {
	rec := &recordingLiveMigratorHandle{}
	m := sched.NewLiveMigrator(rec.fn, nil)
	notif := make(chan db.Notification, 1)
	notif <- db.Notification{Payload: `{"node_id":"alive-1","active":true}`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, notif) }()
	time.Sleep(50 * time.Millisecond)
	close(notif)
	cancel()
	<-done
	if rec.calls.Load() != 0 {
		t.Fatalf("handle called %d times for active=true", rec.calls.Load())
	}
}

func TestLiveMigrator_IgnoresComputeNodeKeys(t *testing.T) {
	rec := &recordingLiveMigratorHandle{}
	m := sched.NewLiveMigrator(rec.fn, nil)
	notif := make(chan db.Notification, 1)
	notif <- db.Notification{Payload: `compute_node_keys`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, notif) }()
	time.Sleep(50 * time.Millisecond)
	close(notif)
	cancel()
	<-done
	if rec.calls.Load() != 0 {
		t.Fatalf("handle called %d times for literal-string payload", rec.calls.Load())
	}
}

func TestLiveMigrator_IgnoresBadJSON(t *testing.T) {
	rec := &recordingLiveMigratorHandle{}
	m := sched.NewLiveMigrator(rec.fn, nil)
	notif := make(chan db.Notification, 1)
	notif <- db.Notification{Payload: `{this is not json}`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, notif) }()
	time.Sleep(50 * time.Millisecond)
	close(notif)
	cancel()
	<-done
	if rec.calls.Load() != 0 {
		t.Fatalf("handle called %d times for bad JSON", rec.calls.Load())
	}
}

func TestLiveMigrator_HandleErrorContinues(t *testing.T) {
	rec := &recordingLiveMigratorHandle{err: errors.New("transient blip")}
	remaining := atomic.Int32{}
	remaining.Store(1)
	rec.errAfter = &remaining
	m := sched.NewLiveMigrator(rec.fn, nil)
	notif := make(chan db.Notification, 2)
	notif <- db.Notification{Payload: `{"node_id":"d1","active":false}`}
	notif <- db.Notification{Payload: `{"node_id":"d2","active":false}`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, notif) }()
	if err := sched.WaitFor(func() bool {
		return rec.calls.Load() == 2
	}, 200*time.Millisecond); err != nil {
		t.Fatalf("handle not called twice within 200ms: %v", err)
	}
	close(notif)
	cancel()
	<-done
	if rec.calls.Load() != 2 {
		t.Fatalf("expected 2 calls (1 errored, 1 success), got %d", rec.calls.Load())
	}
	if len(rec.seen) != 1 || rec.seen[0] != "d2" {
		t.Fatalf("expected d2 in seen (d1 errored), got %v", rec.seen)
	}
}

func TestLiveMigrator_BackToBackSameNode(t *testing.T) {
	rec := &recordingLiveMigratorHandle{}
	m := sched.NewLiveMigrator(rec.fn, nil)
	notif := make(chan db.Notification, 2)
	notif <- db.Notification{Payload: `{"node_id":"d1","active":false}`}
	notif <- db.Notification{Payload: `{"node_id":"d1","active":false}`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, notif) }()
	if err := sched.WaitFor(func() bool {
		return rec.calls.Load() == 2
	}, 200*time.Millisecond); err != nil {
		t.Fatalf("handle not called twice within 200ms: %v", err)
	}
	close(notif)
	cancel()
	<-done
}

func TestLiveMigrator_NilHandlePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("NewLiveMigrator(nil, nil) did not panic")
		}
	}()
	_ = sched.NewLiveMigrator(nil, nil)
}

// waitFor is the package-shared helper from
// pkg/sched/deletion_subscriber_test.go — declared once,
// reused by rebalancer_test.go and this file. See that file
// for the contract.
