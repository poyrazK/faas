// rebalancer_test.go — table-driven tests for Tier A4's
// Rebalancer watcher (ADR-064). Pins the watcher contract
// parallel to pkg/sched/router_watcher_test.go:
//
//   - active=false JSON payload → handle called once with the
//     dead_node_id.
//   - active=true payload → handle NOT called (router_watcher's
//     job; the rebalancer only migrates away from a dead node).
//   - Literal "compute_node_keys" payload → handle NOT called
//     (migration 00076 second-trigger; not a node event).
//   - Malformed JSON → handle NOT called, loop survives, next
//     valid payload is honoured.
//   - handle returns err → watcher logs + continues; next
//     payload is still honoured.
//   - Back-to-back payloads for the same dead node → handle
//     called twice (no batching; the watcher trusts the
//     producer to send only what changed).
//   - Ctx cancel pre-notify → watcher returns within deadline.
//
// The engine-side policy (admission + cooldown + per-tick cap +
// conditional UPDATE + metric + rebalanced notify) is exercised
// separately by pkg/sched/engine_test.go's new
// TestRebalanceOrphanedApps_* cases — this file is the
// "watcher does the right filtering and dispatch" half of the
// contract.

package sched

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

// recordingRebalancerHandle is the test stand-in for
// Engine.RebalanceOrphanedApps. It records every
// (ctx, dead_node_id) pair into seen, bumps calls on each
// invocation, and on the per-test `errAfter` injection returns
// that error (consuming one error call). The atomic counter
// on `calls` is what the table assertions look at.
type recordingRebalancerHandle struct {
	mu       sync.Mutex
	seen     []string
	calls    atomic.Int32
	errAfter *atomic.Int32 // when > 0, the next call returns injectedErr
	err      error
}

func (r *recordingRebalancerHandle) fn(ctx context.Context, deadNodeID string) error {
	r.calls.Add(1)
	r.mu.Lock()
	r.seen = append(r.seen, deadNodeID)
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

// TestRebalancer_DrainPayload_CallsHandle pins the happy
// path: one well-formed active=false notification → exactly
// one handle call with the dead node id.
func TestRebalancer_DrainPayload_CallsHandle(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification, 1)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"fsn-2","active":false}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 1, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 1 {
		t.Fatalf("handle calls = %d, want 1", got)
	}
	if rec.seen[0] != "fsn-2" {
		t.Errorf("seen[0] = %q, want %q", rec.seen[0], "fsn-2")
	}
}

// TestRebalancer_ActiveTruePayload_DropsHandle pins the
// filter: an active=true payload is the router_watcher's job
// (dial target refresh), not the rebalancer's. Reached for the
// case where a node flips active=false→true→false in quick
// succession.
func TestRebalancer_ActiveTruePayload_DropsHandle(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification, 1)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"fsn-2","active":true}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	// Give the watcher a tick to consume; nothing should happen.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 0 {
		t.Errorf("handle calls = %d, want 0 (active=true must NOT trigger a rebalance)", got)
	}
}

// TestRebalancer_ComputeNodeKeysLiteral_NoDispatch pins the
// migration 00076 second-trigger: the literal-string payload
// is parseable as JSON ("compute_node_keys" is a valid JSON
// string), but its shape is not {node_id, active}. The watcher
// must drop it without dispatch — same posture as
// TestWatcher_ComputeNodeKeysLiteral_NoRefresh.
func TestRebalancer_ComputeNodeKeysLiteral_NoDispatch(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification, 1)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `"compute_node_keys"`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 0 {
		t.Errorf("handle calls = %d, want 0 (literal must not trigger a rebalance)", got)
	}
}

// TestRebalancer_MalformedJSON_LoopSurvives pins the
// resilience contract: a malformed payload must not panic
// the loop nor block subsequent valid payloads. Same shape
// as router_watcher_test.go::TestWatcher_MalformedJSON_NoPanic
// — second payload must still land.
func TestRebalancer_MalformedJSON_LoopSurvives(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification, 2)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: "not json {{{",
	}
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"after-malformed","active":false}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 1, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 1 {
		t.Errorf("handle calls = %d, want 1 (malformed dropped, second honoured)", got)
	}
	if rec.seen[0] != "after-malformed" {
		t.Errorf("seen[0] = %q, want %q", rec.seen[0], "after-malformed")
	}
}

// TestRebalancer_HandleError_LogsAndContinues pins the
// failure-resilience contract: a transient PG blip on the
// first call must not stop the loop — the second payload
// is still honoured. Mirrors TestWatcher_RefreshError_LogsAndContinues.
func TestRebalancer_HandleError_LogsAndContinues(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	rec.err = errors.New("simulated PG blip")
	rec.errAfter = &atomic.Int32{}
	rec.errAfter.Store(1) // first call errors; second succeeds

	ch := make(chan db.Notification, 2)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"fsn-1","active":false}`,
	}
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"fsn-2","active":false}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 2, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 2 {
		t.Errorf("handle calls = %d, want 2 (first errors, second still dispatched)", got)
	}
}

// TestRebalancer_BackToBack_DispatchesTwice pins the
// no-batching contract. A flap-loop operator (active=false /
// true / false / true) produces N notifications; the watcher
// trusts the producer and dispatches each one.
func TestRebalancer_BackToBack_DispatchesTwice(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification, 2)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"same","active":false}`,
	}
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"same","active":false}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	waitForCalls(t, rec.calls.Load, 2, time.Second)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 2 {
		t.Errorf("handle calls = %d, want 2 (no batching)", got)
	}
}

// TestRebalancer_CtxCancel_Returns pins the shutdown
// contract. No payloads are sent; the ctx deadline must
// unblock the select. Same shape as router watcher.
func TestRebalancer_CtxCancel_Returns(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification) // unbuffered; nothing sent

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	select {
	case <-done:
		// watcher returned before the test deadline; the 50ms
		// ctx timeout fired.
	case <-time.After(time.Second):
		t.Fatal("rebalancer did not exit on ctx cancel within 1s")
	}

	if got := rec.calls.Load(); got != 0 {
		t.Errorf("handle calls = %d, want 0 (no payload was sent)", got)
	}
}

// TestRebalancer_EmptyNodeID_DropsHandle pins the
// empty-string edge case: json.Unmarshal of an object with a
// missing node_id field yields "". The watcher must drop
// (rather than dispatch a rebalance of every app to
// default-local, which would be a self-DoS).
func TestRebalancer_EmptyNodeID_DropsHandle(t *testing.T) {
	rec := &recordingRebalancerHandle{}
	ch := make(chan db.Notification, 1)
	ch <- db.Notification{
		Channel: db.NotifyComputeNodesChanged,
		Payload: `{"node_id":"","active":false}`,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRebalancer(rec.fn, nil).Run(ctx, ch)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := rec.calls.Load(); got != 0 {
		t.Errorf("handle calls = %d, want 0 (empty node_id must not trigger a rebalance)", got)
	}
}
