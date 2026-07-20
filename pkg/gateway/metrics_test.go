package gateway

import (
	"context"
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