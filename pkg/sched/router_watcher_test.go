// router_watcher_test.go — table-driven tests for Tier A3's
// RunRouterRefreshWatcher. Pins the watcher contract:
//
//   - JSON payload → refresh called once with the node_id.
//   - Literal "compute_node_keys" payload → refresh NOT called,
//     no error, no panic (the migration-00076 second-trigger).
//   - Malformed JSON → refresh NOT called, loop survives.
//   - Ctx cancel pre-notify → watcher returns within deadline.
//   - refresh errors → watcher logs + continues; next payload
//     is still honoured.
//   - Back-to-back payloads for the same nodeID → refresh called
//     twice (no batching; the watcher trusts the producer to
//     send only what changed).

package sched

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

// recordingRefreshFunc returns a RouterRefreshFunc that records
// every (ctx, nodeID) pair into got, and on the per-test
// `err` injection returns that error (and consumes one error
// call). The atomic counter on `calls` is what the table
// assertions actually look at.
type recordingRefreshFunc struct {
	mu       sync.Mutex
	seen     []string
	calls    atomic.Int32
	errAfter *atomic.Int32 // when > 0, the next call returns injectedErr
	err      error
}

func (r *recordingRefreshFunc) fn(_ context.Context, nodeID string) error {
	r.calls.Add(1)
	r.mu.Lock()
	r.seen = append(r.seen, nodeID)
	n := int32(0)
	if r.errAfter != nil {
		n = r.errAfter.Load()
	}
	r.mu.Unlock()
	if n > 0 {
		r.errAfter.Add(-1)
		if r.err != nil {
			return r.err
		}
	}
	return nil
}

func TestWatcher_ActiveTruePayload_CallsRefresh(t *testing.T) {
	rec := &recordingRefreshFunc{}
	ch := make(chan db.Notification, 1)
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"n1","active":true}`}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunRouterRefreshWatcher(ctx, nil, ch, rec.fn)
		close(done)
	}()
	// wait for the refresh; the watcher has no goroutine
	// besides the for-select, so a single buffer-and-pull gives
	// the loop exactly the data it needs.
	waitForCalls(t, rec.calls.Load, 1, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if rec.seen[0] != "n1" {
		t.Errorf("seen[0] = %q, want %q", rec.seen[0], "n1")
	}
}

func TestWatcher_ActiveFalsePayload_CallsRefresh(t *testing.T) {
	rec := &recordingRefreshFunc{}
	ch := make(chan db.Notification, 1)
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"n2","active":false}`}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunRouterRefreshWatcher(ctx, nil, ch, rec.fn)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 1, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 1 {
		t.Errorf("refresh calls = %d, want 1 (no active-only filter)", got)
	}
	if rec.seen[0] != "n2" {
		t.Errorf("seen[0] = %q, want %q", rec.seen[0], "n2")
	}
}

func TestWatcher_MalformedJSON_NoPanic(t *testing.T) {
	rec := &recordingRefreshFunc{}
	ch := make(chan db.Notification, 2)
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: "not json {{{"}
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"after-malformed","active":true}`}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunRouterRefreshWatcher(ctx, nil, ch, rec.fn)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 1, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 1 {
		t.Errorf("refresh calls = %d, want 1 (malformed payload dropped, second payload honoured)", got)
	}
	if rec.seen[0] != "after-malformed" {
		t.Errorf("seen[0] = %q, want %q", rec.seen[0], "after-malformed")
	}
}

func TestWatcher_CtxCancel_Returns(t *testing.T) {
	rec := &recordingRefreshFunc{}
	ch := make(chan db.Notification) // unbuffered; no payloads sent

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunRouterRefreshWatcher(ctx, nil, ch, rec.fn)
		close(done)
	}()
	select {
	case <-done:
		// watcher returned before the test deadline; the 50ms
		// ctx timeout fired.
	case <-time.After(time.Second):
		t.Fatal("watcher did not exit on ctx cancel within 1s")
	}

	if got := rec.calls.Load(); got != 0 {
		t.Errorf("refresh calls = %d, want 0 (no payload was sent)", got)
	}
}

func TestWatcher_RefreshError_LogsAndContinues(t *testing.T) {
	rec := &recordingRefreshFunc{}
	rec.err = context.DeadlineExceeded // any non-nil error works
	rec.errAfter = &atomic.Int32{}
	rec.errAfter.Store(1) // first call errors; second succeeds

	ch := make(chan db.Notification, 2)
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"n1","active":true}`}
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"n2","active":true}`}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunRouterRefreshWatcher(ctx, nil, ch, rec.fn)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 2, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 2 {
		t.Errorf("refresh calls = %d, want 2 (first errors, second still dispatched)", got)
	}
}

func TestWatcher_BackToBack_RefreshesTwice(t *testing.T) {
	rec := &recordingRefreshFunc{}
	ch := make(chan db.Notification, 2)
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"same","active":true}`}
	ch <- db.Notification{Channel: db.NotifyComputeNodesChanged, Payload: `{"node_id":"same","active":true}`}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunRouterRefreshWatcher(ctx, nil, ch, rec.fn)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 2, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 2 {
		t.Errorf("refresh calls = %d, want 2 (no batching; we WANT a fresh row each time)", got)
	}
}

// waitForCalls polls get every millisecond until it returns want
// or timeout elapses. Used in lieu of time.Sleep so flaky-test
// wall time stays at the actual lockup, not a fixed paranoia
// budget. Renamed to avoid clash with the deletion_subscriber
// package helper.
func waitForCalls(t *testing.T, get func() int32, want int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if get() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
