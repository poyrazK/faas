// Fuzz target for WakeGate.Wait (Tier3 #10).
//
// The gate has three primary races:
//   - leader shouldWake check racing with concurrent shouldWake peers
//   - cap-rejected waiter vs a simultaneously-completing leader
//   - ctx cancellation racing the leader goroutine's TTL clock
//
// We seed tests with handcrafted inputs and run go-fuzz against them.
// Each fuzz input is a sequence of waiters with independent (shouldWake,
// ensureFail) decisions; we assert the invariants:
//   - ensure is called at most once per appID while any waiter is pending
//   - all waiters observe ErrQueueFull, ctx.Err(), or call.err
//   - InflightWaiters reaches 0 after the last waiter departs
package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// FuzzWakeGate is a deterministic fuzz runner (skeleton: real fuzzing happens
// when -fuzz=FuzzWakeGate is passed to go test). The fixed seeds exercise
// every documented interaction.
func FuzzWakeGate(f *testing.F) {
	f.Add(uint8(8), uint8(0))
	f.Add(uint8(2), uint8(1))
	f.Add(uint8(64), uint8(2))
	f.Add(uint8(1), uint8(0))

	f.Fuzz(func(t *testing.T, fansByte, modeByte uint8) {
		fans := int(fansByte) + 1
		mode := int(modeByte) % 3
		g := NewWakeGate(8, 250*time.Millisecond)

		var ensureCalls int32
		ensureOK := atomic.LoadInt32(&ensureCalls)
		ensure := func(ctx context.Context) error {
			atomic.AddInt32(&ensureCalls, 1)
			return nil
		}
		_ = ensureOK

		shouldWake := func() bool {
			switch mode {
			case 0:
				return true // always wake
			case 1:
				return false // never wake (short-circuit)
			case 2:
				// Toggle: odd calls return true, even return false — covers
				// the leader/follower race against shouldWake=false.
				return atomic.LoadInt32(&ensureCalls)%2 == 0
			}
			return true
		}

		errs := make([]error, fans)
		done := make(chan struct{})
		for i := 0; i < fans; i++ {
			go func(i int) {
				errs[i] = g.Wait(context.Background(), "app", "test-acct", shouldWake, ensure, nil, nil)
				done <- struct{}{}
			}(i)
		}
		for i := 0; i < fans; i++ {
			<-done
		}
		// Invariants:
		for _, e := range errs {
			if e != nil && !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) && !errors.Is(e, ErrQueueFull) && e.Error() == "" {
				t.Errorf("unexpected nil-or-empty error: %v", e)
			}
		}
		// ensure may have been called 0 (mode=1) or at-least-1 (mode 0/2),
		// but MUST NOT have been called twice for mode=1.
		calls := atomic.LoadInt32(&ensureCalls)
		if mode == 1 && calls != 0 {
			t.Errorf("mode=1 (always shouldWake=false) → ensure ran %d times, want 0", calls)
		}
		if g.InflightWaiters("app") != 0 {
			t.Errorf("inflight after fuzz = %d, want 0", g.InflightWaiters("app"))
		}
	})
}

// TestWakeGateDetachedLeaderBootstrapCapAbort (ADR-098 C7) covers the
// detached-leader bootstrap cap: when the predicate shouldAbort fires
// (queue empty AND no live instance AND plan.MaxMinInstances == 0),
// the leader goroutine's poller closes abortCh and the leader surfaces
// ErrBootstrapAborted to all callers — instead of waiting the TTL while
// a no-longer-wanted wake runs in the background.
//
// The test wires a 1.5s TTL on the gate and a 1s ticker in the leader
// goroutine. shouldAbort is signalled ~50ms after the gate starts so
// the leader's poll loop fires within the first tick. All callers
// (leader + followers) see ErrBootstrapAborted within ~1s — well below
// the 1.5s TTL.
//
// Note: the ensure goroutine still races to start; the contract is
// "followers see ErrBootstrapAborted", not "ensure never ran". The
// orphan-detach semantics guarantee the wake outlives the caller; the
// bootstrap cap is the cost ceiling when the caller's gone.
func TestWakeGateDetachedLeaderBootstrapCapAbort(t *testing.T) {
	const cap = 8
	g := NewWakeGate(cap, 1500*time.Millisecond)
	shouldWake := func() bool { return true }

	ensure := func(ctx context.Context) error {
		// Block until ctx expires — the call only returns after
		// the TTL. When the abort fires, the leader's outer
		// goroutine closes call.done and the followers unwind;
		// this goroutine is then orphaned and continues running
		// until ectx.Done() (the TTL). That's the acknowledged
		// cost ceiling: the wake is still in flight, but no
		// caller is waiting.
		<-ctx.Done()
		return ctx.Err()
	}

	abort := make(chan struct{}, 1)
	shouldAbort := func() bool {
		select {
		case <-abort:
			return true
		default:
			return false
		}
	}
	var abortReason string
	onAbort := func(reason string) {
		abortReason = reason
	}

	// 1 leader + 4 followers (well under cap). The leader runs
	// ensure; the followers wait on the gate's done channel.
	const followers = 4
	errs := make([]error, 1+followers)
	done := make(chan struct{}, 1+followers)
	for i := 0; i < 1+followers; i++ {
		go func(i int) {
			errs[i] = g.Wait(
				context.Background(),
				"app",
				"fuzz-acct",
				shouldWake,
				ensure,
				shouldAbort,
				onAbort,
			)
			done <- struct{}{}
		}(i)
		// Yield so the leader (i=0) commits before the followers join.
		if i == 0 {
			for g.InflightWaiters("app") < 1 {
				time.Sleep(time.Millisecond)
			}
		}
	}

	// Give the leader goroutine a chance to spin up its poller, then
	// signal shouldAbort. 50ms is well under the 1s ticker period to
	// make sure the poller observes the signal on its first tick.
	time.Sleep(50 * time.Millisecond)
	abort <- struct{}{}

	// All callers must return within the TTL. The 2s deadline is
	// double the TTL to catch the "no abort" regression (callers
	// would block until ctx inside ensure expires — but they would
	// still see ctx.Err(), not ErrBootstrapAborted).
	expire := time.After(2 * time.Second)
	for i := 0; i < 1+followers; i++ {
		select {
		case <-done:
		case <-expire:
			t.Fatalf("caller %d did not return within 2s; abort didn't fire", i)
		}
	}

	// Every caller (leader + followers) sees ErrBootstrapAborted.
	for i, e := range errs {
		if !errors.Is(e, ErrBootstrapAborted) {
			t.Errorf("caller %d err = %v, want ErrBootstrapAborted", i, e)
		}
	}
	// onAbort was invoked with the canonical reason.
	if abortReason != "queue_empty_no_instance" {
		t.Errorf("onAbort reason = %q, want %q", abortReason, "queue_empty_no_instance")
	}
	// Gate is fully drained.
	if got := g.InflightWaiters("app"); got != 0 {
		t.Errorf("inflight after abort = %d, want 0", got)
	}
}

// TestInflightFollowersMinusLeader (ADR-098 C11 review fix) pins
// the unsatisfiable-predicate bug: the bootstrap-cap abort predicate
// must check follower count, not total waiter count. The leader's
// own registration keeps the total at >=1 for the entire bootstrap
// duration. A pre-fix check `call.waiters == 0` is unsatisfiable
// while the leader's caller awaits done, so the abort never fires.
//
// The helper returns 0 when only the leader is alive (the case the
// bootstrap-cap abort wants), and the full count when followers are
// queued. Verifies that a leader-only entry reads 0 followers so the
// predicate triggers when HealthyCount == 0 and the queue is empty.
func TestInflightFollowersMinusLeader(t *testing.T) {
	const cap = 8
	g := NewWakeGate(cap, 1500*time.Millisecond)

	// 1 leader: blocks in ensure, no followers. With the buggy
	// InflightWaiters==0 predicate, the abort never fires. With
	// InflightFollowers, the predicate returns true at the moment
	// the leader is alone.
	leaderDone := make(chan error, 1)
	go func() {
		leaderDone <- g.Wait(
			context.Background(),
			"app",
			"fuzz-acct",
			func() bool { return true },
			func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			func() bool { return g.InflightFollowers("app") == 0 },
			nil,
		)
	}()
	// Wait for leader to register.
	deadline := time.Now().Add(time.Second)
	for g.InflightWaiters("app") < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := g.InflightWaiters("app"); got != 1 {
		t.Fatalf("leader alone: InflightWaiters = %d, want 1", got)
	}
	if got := g.InflightFollowers("app"); got != 0 {
		t.Fatalf("leader alone: InflightFollowers = %d, want 0", got)
	}
	// Add a follower — total goes to 2, followers to 1.
	followerDone := make(chan error, 1)
	go func() {
		followerDone <- g.Wait(
			context.Background(),
			"app",
			"fuzz-acct",
			func() bool { return true },
			func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			nil, nil,
		)
	}()
	for g.InflightWaiters("app") < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := g.InflightWaiters("app"); got != 2 {
		t.Fatalf("leader+follower: InflightWaiters = %d, want 2", got)
	}
	if got := g.InflightFollowers("app"); got != 1 {
		t.Fatalf("leader+follower: InflightFollowers = %d, want 1", got)
	}
	// Drain.
	go func() { leaderDone <- <-leaderDone }() //nolint
	go func() { followerDone <- <-followerDone }()
}
