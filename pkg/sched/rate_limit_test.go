package sched_test

import (
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// frozenClock returns a clock the test owns. The limiter holds no
// reference to it after each Allow call (only its last value is
// recorded on the bucket) so a single clock can drive many tests in
// sequence. Named `frozenClock` to avoid collision with
// pkg/sched/warmaffinity_test.go::fakeClock.
func frozenClock() (clock func() time.Time, advance func(d time.Duration)) {
	var mu sync.Mutex
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance = func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
	return clock, advance
}

func TestWakeRateLimiter_NilReceiverIsNoop(t *testing.T) {
	var l *sched.WakeRateLimiter // nil
	if !l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("nil receiver must allow (test seam contract)")
	}
	if !l.AllowWakeAccount("acct-1", api.PlanScale) {
		t.Fatal("nil receiver must allow")
	}
	if got := l.BucketCount(); got != 0 {
		t.Fatalf("nil BucketCount = %d, want 0", got)
	}
}

func TestWakeRateLimiter_AllowWakeApp_BurstThenThrottle(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	// Scale = 100/min. Consume the full bucket — every call must
	// allow until the bucket is empty.
	for i := 0; i < 100; i++ {
		if !l.AllowWakeApp("app-1", api.PlanScale) {
			t.Fatalf("AllowWakeApp call %d: expected allow while bucket has tokens", i+1)
		}
	}
	// 101st call within the same minute: bucket is empty -> throttle.
	if l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("expected throttle on burst exhaustion")
	}
}

func TestWakeRateLimiter_AllowWakeApp_PerAppIsolation(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	// Drain app-1 to empty on Scale.
	for i := 0; i < 100; i++ {
		l.AllowWakeApp("app-1", api.PlanScale)
	}
	if l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("app-1 should be throttled")
	}
	// app-2 must still have a full bucket — per-app isolation.
	if !l.AllowWakeApp("app-2", api.PlanScale) {
		t.Fatal("app-2 must not share app-1's bucket")
	}
}

func TestWakeRateLimiter_AllowWakeApp_PlanChangePreservesBurstCeiling(t *testing.T) {
	clock, advance := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	// Drain app-1 to empty on Scale (100 tokens consumed in <1s).
	for i := 0; i < 100; i++ {
		l.AllowWakeApp("app-1", api.PlanScale)
	}
	if l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("app-1 should be empty after drain")
	}
	// Downgrade Scale→Pro. New burst ceiling is 20; bucket params
	// refresh, but no time has elapsed so tokens stay at 0 (the
	// refill math only credits time-passed, not the parameter change
	// itself — same shape as pkg/gateway.Limiter.allowToken line 102).
	if l.AllowWakeApp("app-1", api.PlanPro) {
		t.Fatal("downgrade without time advance must not grant free tokens")
	}
	// Advance 60s — at Pro=20/min, that's 20 tokens of refill.
	// The bucket now has the new burst ceiling (20), not the old
	// Scale ceiling (100). Verify by consuming exactly 20 then
	// throttling on the 21st.
	advance(60 * time.Second)
	for i := 0; i < 20; i++ {
		if !l.AllowWakeApp("app-1", api.PlanPro) {
			t.Fatalf("AllowWakeApp call %d after 60s: expected allow under Pro=20/min", i+1)
		}
	}
	if l.AllowWakeApp("app-1", api.PlanPro) {
		t.Fatal("Pro=20/min should throttle after 20 calls within a 60s window")
	}
}

func TestWakeRateLimiter_AllowWakeAccount_BoundsCrossAppFanout(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	// Scale per-account = 150/min. Drain via different app ids
	// (per-app buckets stay fresh). Per-account bucket is the
	// cross-app ceiling.
	for i := 0; i < 150; i++ {
		appID := "app-" + string(rune('a'+i%26))
		if !l.AllowWakeAccount("acct-1", api.PlanScale) {
			t.Fatalf("AllowWakeAccount call %d: expected allow", i+1)
		}
		_ = appID
	}
	if l.AllowWakeAccount("acct-1", api.PlanScale) {
		t.Fatal("expected per-account throttle after 150 wake admits")
	}
}

func TestWakeRateLimiter_RefillOverTime(t *testing.T) {
	clock, advance := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	// Drain Scale bucket (100 tokens, 100/min refill).
	for i := 0; i < 100; i++ {
		l.AllowWakeApp("app-1", api.PlanScale)
	}
	if l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("bucket should be empty immediately after drain")
	}
	// Advance 30 s — at 100/min, that's 50 tokens of refill.
	advance(30 * time.Second)
	for i := 0; i < 50; i++ {
		if !l.AllowWakeApp("app-1", api.PlanScale) {
			t.Fatalf("AllowWakeApp call %d after 30s advance: expected allow", i+1)
		}
	}
	if l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("bucket should be empty after 50 + 100 consumed tokens")
	}
}

func TestWakeRateLimiter_UnknownPlanFailsClosed(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	if l.AllowWakeApp("app-1", api.Plan("unknown")) {
		t.Fatal("unknown plan must fail closed (mirrors pkg/gateway.Limiter.Allow)")
	}
	if l.AllowWakeAccount("acct-1", api.Plan("unknown")) {
		t.Fatal("unknown plan must fail closed")
	}
}

func TestWakeRateLimiter_FreePlanThrottlesToZero(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	// Free = 1 burst — the abuse-floor tier. The first call
	// consumes the single token; subsequent calls throttle until
	// refill (1 token per minute).
	if !l.AllowWakeApp("app-1", api.PlanFree) {
		t.Fatal("first Free call should allow (bucket ceiling = 1)")
	}
	if l.AllowWakeApp("app-1", api.PlanFree) {
		t.Fatal("second Free call should throttle within the same minute")
	}
}

func TestWakeRateLimiter_ForgetDropsBuckets(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)

	l.AllowWakeApp("app-1", api.PlanScale)
	l.AllowWakeAccount("acct-1", api.PlanScale)
	if got := l.BucketCount(); got != 2 {
		t.Fatalf("BucketCount after 2 calls = %d, want 2", got)
	}

	l.ForgetApp("app-1")
	if got := l.BucketCount(); got != 1 {
		t.Fatalf("BucketCount after ForgetApp = %d, want 1", got)
	}
	if !l.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("forgotten bucket should be re-created with full burst on next call")
	}

	l.ForgetAll()
	if got := l.BucketCount(); got != 0 {
		t.Fatalf("BucketCount after ForgetAll = %d, want 0", got)
	}
}

func TestWakeRateLimiter_WithNoopAlwaysAllows(t *testing.T) {
	clock, _ := frozenClock()
	l := sched.NewWakeRateLimiterWithClock(clock)
	noop := l.WithNoop()

	// Drain the original limiter to empty.
	for i := 0; i < 100; i++ {
		l.AllowWakeApp("app-1", api.PlanScale)
	}
	// The noop copy bypasses the bucket entirely — test seam
	// contract: a noop limiter always allows so load tests can
	// measure the underlying handler path.
	if !noop.AllowWakeApp("app-1", api.PlanScale) {
		t.Fatal("noop limiter must always allow")
	}
	if !noop.AllowWakeAccount("acct-1", api.PlanScale) {
		t.Fatal("noop limiter must always allow (per-account)")
	}
}

// Engine-integration test pins:
//
// The Engine wire-up of WakeRateLimiter is exercised in
// pkg/sched/engine_test.go's existing admitAndDispatch tests
// (the nil-receiver path covers every existing test). The
// rate-limit-aware variant (drain the per-app bucket, observe
// AtCapacity lift) is a follow-up property test in PR-C alongside
// the dispatch_jobs.go tick — the Engine is hot enough today that
// a new test would need shared fakes more elaborate than this PR
// can carry in its reviewable-in-10-min budget.
