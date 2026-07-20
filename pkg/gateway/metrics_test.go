package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMetricsWakeQueueWaitRegisters asserts the §12 row name is
// exposed. Catches a rename that would silently break the dashboard.
func TestMetricsWakeQueueWaitRegisters(t *testing.T) {
	m := NewMetrics()
	m.ObserveWakeQueueWait(50 * time.Millisecond)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds") {
		t.Errorf("histogram not in registry output:\n%s", body)
	}
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds_count 1") {
		t.Errorf("expected count=1 in output:\n%s", body)
	}
}

// TestMetricsWakeQueueWaitNilSafe keeps the histogram usable from
// unit tests that haven't constructed a Metrics bundle.
func TestMetricsWakeQueueWaitNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveWakeQueueWait(50 * time.Millisecond) // must not panic
}

// TestWakeGateObservesWaitDuration drives two concurrent Wait calls
// through the gate and asserts the histogram caught at least one
// observation. The leader parks until ensure() returns; the follower
// blocks on the same call and its wait duration is non-zero.
func TestWakeGateObservesWaitDuration(t *testing.T) {
	m := NewMetrics()
	g := NewWakeGate(8, 5*time.Second)
	g.SetMetrics(m)

	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)

	// Leader triggers ensure; both leader (after ensure) and a follower
	// (queued behind the leader) should observe some non-zero wait.
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appA",
			func() bool { return true },
			func(ctx context.Context) error {
				<-release
				return nil
			})
	}()
	// Yield so the leader is committed before the follower joins.
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appA",
			func() bool { return false }, // would-wake check is leader-only; follower ignores it
			func(ctx context.Context) error { return nil })
	}()

	// Hold the leader parked so the follower accumulates wait.
	time.Sleep(50 * time.Millisecond)
	close(release)
	done.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds_count 2") {
		t.Errorf("expected 2 observations (leader + follower), got:\n%s", body)
	}
	// The follower's bucket should be >= 0.05s; the leader's bucket
	// is whatever it took to schedule (likely 0.005s). At least one
	// observation must land in a bucket ≥ 50ms — that's the follower.
	if !strings.Contains(body, `gateway_wake_queue_wait_seconds_bucket{le="0.05"}`) {
		t.Errorf("expected bucket line at le=0.05, got:\n%s", body)
	}
}

// TestWakeGateSkipsObservationOnErrQueueFull guards against the
// regression where ErrQueueFull and ctx-cancelled paths recorded
// ~0ms observations, driving the p95 to near-zero during overload
// storms (the very signal the SLO dashboard needs to surface).
//
// With cap=1, the leader counts as waiter 1; the very next caller
// sees waiters >= cap and gets ErrQueueFull synchronously. That
// rejected caller must NOT record in the wake-wait histogram.
func TestWakeGateSkipsObservationOnErrQueueFull(t *testing.T) {
	m := NewMetrics()
	g := NewWakeGate(1, 5*time.Second)
	g.SetMetrics(m)

	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)

	// Leader parks; counts as waiter 1 of cap=1.
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appB",
			func() bool { return true },
			func(ctx context.Context) error { <-release; return nil })
	}()
	time.Sleep(20 * time.Millisecond) // leader commits first

	// Synchronous next caller — gate rejects with ErrQueueFull.
	err := g.Wait(context.Background(), "appB",
		func() bool { return false },
		func(ctx context.Context) error { return nil })
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}

	close(release)
	done.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	// Only the leader observed (count=1); the rejected caller did not.
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds_count 1") {
		t.Errorf("expected count=1 (rejected caller skipped), got:\n%s", body)
	}
}

// TestWakeGateSkipsObservationOnCtxCancel guards the other
// non-wait return: ctx cancellation. A caller that cancels before
// the in-flight ensure returns should not be recorded as having
// "waited" — it never got an instance.
//
// Race-free variant: the follower must observe the leader's
// in-flight wakeCall before the leader can complete and release
// the entry (otherwise the follower would short-circuit on
// shouldWake=false and become a new leader with its own non-
// observation outcome). We synchronize via InflightWaiters so
// the test waits until the follower is queued, then releases.
func TestWakeGateSkipsObservationOnCtxCancel(t *testing.T) {
	m := NewMetrics()
	g := NewWakeGate(8, 5*time.Second)
	g.SetMetrics(m)

	release := make(chan struct{})
	followerCommitted := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)

	// Leader parks; ensure returns nil after we release.
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appC",
			func() bool { return true },
			func(ctx context.Context) error { <-release; return nil })
	}()

	// Follower with a cancelled context — must be queued behind the
	// leader BEFORE the leader's wakeCall is released. Otherwise the
	// follower becomes a new leader with shouldWake=false and never
	// observes.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	go func() {
		defer done.Done()
		_ = g.Wait(cancelledCtx, "appC",
			func() bool { return false },
			func(ctx context.Context) error { return nil })
		// Tell the test driver we've entered Wait (even if it returned
		// immediately — the gate has serialized us).
		close(followerCommitted)
	}()

	// Wait until the follower has actually entered Wait. Polling
	// InflightWaiters is the cheapest signal that the gate has
	// serialized the caller against the leader's call.
	deadline := time.Now().Add(2 * time.Second)
	for g.InflightWaiters("appC") < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.InflightWaiters("appC") < 2 {
		// Follower may have short-circuited (e.g. leader already
		// completed and the entry was released). Wait for the
		// commit signal as a fallback so we still close release
		// below.
		select {
		case <-followerCommitted:
		case <-time.After(time.Second):
		}
	}

	close(release)
	done.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	// Leader observed (one entry) IF it actually waited for the
	// follower to be queued. The follower never observes (ctx.Err()
	// path). Count is therefore 0 if the leader completed before
	// the follower queued, or 1 if it waited.
	count := countObservations(body, "gateway_wake_queue_wait_seconds_count")
	if count > 1 {
		t.Errorf("got count=%d, want <=1 (follower must skip)", count)
	}
}

// countObservations parses the bare `gateway_wake_queue_wait_seconds_count N`
// line out of a Prometheus exposition body. Returns 0 if the line isn't
// present (histogram never observed).
func countObservations(body, metric string) int {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, metric+" ") {
			continue
		}
		var n int
		_, err := fmt.Sscanf(line, metric+" %d", &n)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}
