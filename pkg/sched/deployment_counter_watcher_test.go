// Tests for the DeploymentCounterWatcher (issue #555 PR-6).
//
// The watcher subscribes to the in-process Platform `wake` topic
// and resets the per-deployment sampling counter on the
// "last live instance parked" transition. Tests exercise the
// handle() decode path directly (no need to spin up the goroutine)
// so assertions target the per-event logic in isolation. A single
// end-to-end test starts the goroutine and verifies the reset
// fires after a real ParkCompleted event lands on the broadcaster.
package sched_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire/otelinit"
)

// stubStore is a minimal LiveInstanceCounter for the watcher's
// resync path. Only CountLiveInstancesByDeployment is exercised
// (the watcher takes a narrow interface, not the full
// state.Store, so no other methods need to be implemented).
type stubStore struct {
	mu     sync.Mutex
	counts map[string]int
}

func newStubStore() *stubStore {
	return &stubStore{counts: make(map[string]int)}
}

func (s *stubStore) CountLiveInstancesByDeployment(_ context.Context, depID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[depID], nil
}

// parkCompletedPayload is the JSON shape the Platform writes onto
// the broadcaster (events/platform.go:177-184). Tests build a
// payload directly so the watcher's decoder is exercised end-to-end.
func parkCompletedPayload(depID string) []byte {
	env := map[string]any{
		"at":    time.Now().UTC(),
		"kind":  events.WakeParkCompleted,
		"actor": "schedd",
		"data": map[string]any{
			"app_id":        "app-555",
			"deployment_id": depID,
			"instance_id":   "ins-555-1",
		},
	}
	b, _ := json.Marshal(env)
	return b
}

// parkStartedPayload covers the negative case: a ParkStarted event
// must NOT trigger a counter reset (the dual pair logic is by
// ParkCompleted only).
func parkStartedPayload(depID string) []byte {
	env := map[string]any{
		"at":    time.Now().UTC(),
		"kind":  events.WakeParkStarted,
		"actor": "schedd",
		"data": map[string]any{
			"app_id":        "app-555",
			"deployment_id": depID,
			"instance_id":   "ins-555-1",
		},
	}
	b, _ := json.Marshal(env)
	return b
}

// quietLogger discards everything except Error-level records so
// test output stays clean.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestWatcher_HandleParkCompleted_ResetsWhenCountReachesZero
// pins the happy path: a single ParkCompleted for a deployment
// with a 1-instance live-count cache triggers counter.Reset.
func TestWatcher_HandleParkCompleted_ResetsWhenCountReachesZero(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())
	(*

	// Pre-populate the cache to 1 (the canonical "one live
	// instance for this deployment" state). Use resync with
	// stub state so we can avoid coupling to the engine.
	w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 1
	(*w.Mu()).Unlock()

	// Push the counter up to 5 (synthetic — the sampler would
	// have done this in production). The reset is observable
	// by virtue of post-reset Observe returning (1, true)
	// instead of (6, false).
	for i := 0; i < 5; i++ {
		counter.Observe("dep-A")
	}
	if n, _ := counter.Observe("dep-A"); n != 6 {
		t.Fatalf("setup: pre-reset counter = %d, want 6", n)
	}

	w.Handle(parkCompletedPayload("dep-A"))

	// Counter must be reset — next Observe starts from 1.
	n, ok := counter.Observe("dep-A")
	if n != 1 {
		t.Errorf("post-reset Observe count = %d, want 1", n)
	}
	if !ok {
		t.Error("post-reset Observe sampled = false, want true (window opens again)")
	}
}

// TestWatcher_HandleParkCompleted_IgnoresMidFleetPark pins the
// "more than one live instance" case: a ParkCompleted must NOT
// trigger a reset while at least one other instance is still
// live for the deployment.
func TestWatcher_HandleParkCompleted_IgnoresMidFleetPark(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())
	(*

	// Two live instances for dep-A.
	w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 2
	(*w.Mu()).Unlock()
	counter.Observe("dep-A")
	counter.Observe("dep-A")
	counter.Observe("dep-A")

	w.Handle(parkCompletedPayload("dep-A"))

	// Counter must NOT be reset; the post-handle Observe
	// continues from where it was.
	n, _ := counter.Observe("dep-A")
	if n != 4 {
		t.Errorf("mid-fleet Observe = %d, want 4 (no reset)", n)
	}
}

// TestWatcher_HandleParkStarted_Ignored pins that a ParkStarted
// event does NOT trigger a reset (only ParkCompleted does).
func TestWatcher_HandleParkStarted_Ignored(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())
	(*w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 1
	(*w.Mu()).Unlock()
	counter.Observe("dep-A")
	counter.Observe("dep-A")

	w.Handle(parkStartedPayload("dep-A"))
	(*

	// Cache must still hold 1 (ParkStarted doesn't decrement),
	// and counter must NOT be reset.
	w.Mu()).Lock()
	cache := (*w.LiveCount())["dep-A"]
	(*w.Mu()).Unlock()
	if cache != 1 {
		t.Errorf("post-ParkStarted cache = %d, want 1 (no decrement)", cache)
	}
	n, _ := counter.Observe("dep-A")
	if n != 3 {
		t.Errorf("post-ParkStarted Observe = %d, want 3 (no reset)", n)
	}
}

// TestWatcher_HandleEmptyDeploymentID_Noop pins that legacy
// ParkCompleted emits (without the deployment_id field added in
// PR-6) are silently skipped — the cache is untouched and no
// reset is attempted.
func TestWatcher_HandleEmptyDeploymentID_Noop(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())
	(*w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 1
	(*w.Mu()).Unlock()
	counter.Observe("dep-A")

	// Hand-build a ParkCompleted payload WITHOUT deployment_id.
	env := map[string]any{
		"at":   time.Now().UTC(),
		"kind": events.WakeParkCompleted,
		"data": map[string]any{
			"app_id":      "app-555",
			"instance_id": "ins-555-1",
		},
	}
	b, _ := json.Marshal(env)
	w.Handle(b)
	(*w.Mu()).Lock()
	cache := (*w.LiveCount())["dep-A"]
	(*w.Mu()).Unlock()
	if cache != 1 {
		t.Errorf("post-legacy-park cache = %d, want 1 (no decrement on missing depID)", cache)
	}
}

// TestWatcher_Resync_RemovesDrainedDeployment pins that the
// periodic resync removes a deployment from the cache when the
// store reports zero live instances for it. The corresponding
// counter is reset as a side-effect (defensive: a missed
// ParkCompleted event during the window would otherwise leave
// the counter stale).
func TestWatcher_Resync_RemovesDrainedDeployment(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())
	(*w.ResyncEvery()) = time.Hour
	(* // keep the ticker out of the test

	// Pre-populate the cache; store reports 0 for dep-A.
	w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 5
	(*w.Mu()).Unlock()
	store.counts["dep-A"] = 0
	counter.Observe("dep-A")
	counter.Observe("dep-A")

	if err := w.Resync(context.Background()); err != nil {
		t.Fatalf("resync: %v", err)
	}
	(*w.Mu()).Lock()
	_, has := (*w.LiveCount())["dep-A"]
	(*w.Mu()).Unlock()
	if has {
		t.Error("dep-A still in cache after resync reports 0; want removed")
	}
	// Counter must be reset.
	n, _ := counter.Observe("dep-A")
	if n != 1 {
		t.Errorf("post-resync Observe = %d, want 1 (reset)", n)
	}
}

// TestWatcher_Resync_KeepsAliveDeployment pins the negative
// case: when the store reports >0 live instances, the cache
// value is updated to the SQL truth (NOT reset to the SQL
// truth — a deployment with cache=5 and store=3 must come out
// at 3, not 0, so the next ParkCompleted has the right
// post-decrement value).
func TestWatcher_Resync_KeepsAliveDeployment(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())
	(*w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 5
	(*w.Mu()).Unlock()
	store.counts["dep-A"] = 3

	if err := w.Resync(context.Background()); err != nil {
		t.Fatalf("resync: %v", err)
	}
	(*w.Mu()).Lock()
	cache := (*w.LiveCount())["dep-A"]
	(*w.Mu()).Unlock()
	if cache != 3 {
		t.Errorf("post-resync cache = %d, want 3 (re-anchored to store)", cache)
	}
	// Counter must NOT be reset.
	n, _ := counter.Observe("dep-A")
	if n != 1 {
		t.Errorf("post-resync Observe = %d, want 1 (no reset, counter preserved)", n)
	}
}

// TestWatcher_Run_NilBroadcasterIsNoop pins that constructing
// the watcher without a broadcaster is a no-op: Run returns when
// ctx is cancelled, no goroutine leaks.
func TestWatcher_Run_NilBroadcasterIsNoop(t *testing.T) {
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(nil, counter, store, quietLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run returned %v, want nil/timeout", err)
		}
	}
}

// TestWatcher_Run_EndToEnd_ResetsOnLastPark spins up the full
// pipeline: a Broadcaster + a real Run goroutine + a synthetic
// ParkCompleted publish. Asserts the counter resets within a
// short window.
func TestWatcher_Run_EndToEnd_ResetsOnLastPark(t *testing.T) {
	bc := events.New()
	counter := otelinit.NewDeploymentCounter(100)
	store := newStubStore()
	w := sched.NewDeploymentCounterWatcher(bc, counter, store, quietLogger())
	(*w.ResyncEvery()) = time.Hour
	(* // keep the ticker out of the test

	// Pre-load the cache so the next ParkCompleted will drain it.
	w.Mu()).Lock()
	(*w.LiveCount())["dep-A"] = 1
	(*w.Mu()).Unlock()
	counter.Observe("dep-A")
	counter.Observe("dep-A")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Publish a ParkCompleted event. The Platform would normally
	// do this; we synthesise the publish so the test stays
	// self-contained.
	bc.PublishTopic(events.TopicWake, parkCompletedPayload("dep-A"))

	// Wait for the watcher to apply the event. Poll briefly
	// because the subscribe path is async (channel buffer + select).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, _ := counter.Observe("dep-A")
		if n == 1 {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done
	t.Fatal("counter did not reset within 2s after ParkCompleted publish")
}
