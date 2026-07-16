package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- rate limiter ----------------------------------------------------------

func TestLimiterBurstThenRefill(t *testing.T) {
	l := NewLimiter()
	clock := time.Now()
	l.now = func() time.Time { return clock }

	// Free plan: 5 rps, burst 20. First 20 allowed, 21st denied.
	allowed := 0
	for i := 0; i < 25; i++ {
		if l.Allow("app", api.PlanFree) {
			allowed++
		}
	}
	if allowed != 20 {
		t.Errorf("burst allowed %d, want 20", allowed)
	}
	// After 1 second, ~5 tokens refill.
	clock = clock.Add(time.Second)
	refill := 0
	for i := 0; i < 10; i++ {
		if l.Allow("app", api.PlanFree) {
			refill++
		}
	}
	if refill != 5 {
		t.Errorf("refill after 1s = %d, want 5 (Free rps)", refill)
	}
}

func TestLimiterIsPerApp(t *testing.T) {
	l := NewLimiter()
	clock := time.Now()
	l.now = func() time.Time { return clock }
	// Drain app A's burst.
	for i := 0; i < 20; i++ {
		l.Allow("A", api.PlanFree)
	}
	if l.Allow("A", api.PlanFree) {
		t.Error("A should be rate-limited")
	}
	if !l.Allow("B", api.PlanFree) {
		t.Error("B should have its own bucket")
	}
}

func TestLimiterCapsAtBurst(t *testing.T) {
	l := NewLimiter()
	clock := time.Now()
	l.now = func() time.Time { return clock }
	l.Allow("app", api.PlanPro)  // create bucket (burst 500)
	clock = clock.Add(time.Hour) // huge idle
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.Allow("app", api.PlanPro) {
			allowed++
		}
	}
	if allowed > 500 {
		t.Errorf("tokens should cap at burst 500, allowed %d", allowed)
	}
}

func TestLimiterForgetAll(t *testing.T) {
	l := NewLimiter()
	l.Allow("a", api.PlanFree)
	l.Allow("b", api.PlanFree)
	l.Allow("c", api.PlanPro)
	if l.BucketCount() != 3 {
		t.Fatalf("setup: BucketCount = %d, want 3", l.BucketCount())
	}
	dropped := l.ForgetAll()
	if dropped != 3 {
		t.Errorf("ForgetAll dropped %d, want 3", dropped)
	}
	if l.BucketCount() != 0 {
		t.Errorf("after ForgetAll BucketCount = %d, want 0", l.BucketCount())
	}
}

// --- route cache -----------------------------------------------------------

func TestRouteCacheGetPut(t *testing.T) {
	c := NewRouteCache(10)
	if _, ok := c.Get("x.apps.dom"); ok {
		t.Error("empty cache should miss")
	}
	c.Put("x.apps.dom", "app-1")
	if id, ok := c.Get("x.apps.dom"); !ok || id != "app-1" {
		t.Errorf("got %q, %v", id, ok)
	}
}

func TestRouteCacheLRUEviction(t *testing.T) {
	c := NewRouteCache(2)
	c.Put("a", "1")
	c.Put("b", "2")
	_, _ = c.Get("a") // a now MRU
	c.Put("c", "3")   // evicts b (LRU)
	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a should survive (was promoted)")
	}
	if c.Len() != 2 {
		t.Errorf("len = %d, want cap 2", c.Len())
	}
}

func TestRouteCacheInvalidate(t *testing.T) {
	c := NewRouteCache(10)
	c.Put("a", "1")
	c.Invalidate("a")
	if _, ok := c.Get("a"); ok {
		t.Error("invalidated route should miss")
	}
}

// --- wake gate -------------------------------------------------------------

func TestWakeGateSingleFlight(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	var calls int32
	ensure := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond) // simulate a wake
		return nil
	}
	// Gate-only test: there's no backend, so always wake.
	shouldWake := func() bool { return true }

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Wait(context.Background(), "app", shouldWake, ensure); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("100 concurrent waiters should trigger 1 wake, got %d", got)
	}
}

func TestWakeGatePropagatesEnsureError(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	shouldWake := func() bool { return true }
	err := g.Wait(context.Background(), "app", shouldWake, func(context.Context) error {
		return fmt.Errorf("no capacity")
	})
	if err == nil {
		t.Error("ensure error should propagate to the waiter (→ 503)")
	}
}

func TestWakeGateCapReturnsQueueFull(t *testing.T) {
	g := NewWakeGate(3, 5*time.Second)
	shouldWake := func() bool { return true }
	release := make(chan struct{})
	// Leader blocks so followers accumulate.
	go func() {
		_ = g.Wait(context.Background(), "app", shouldWake, func(context.Context) error {
			<-release
			return nil
		})
	}()
	// Wait for the leader to register.
	for g.InflightWaiters("app") < 1 {
		time.Sleep(time.Millisecond)
	}

	// Fill remaining capacity (cap 3 → 2 more followers ok), then overflow.
	errs := make([]error, 5)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.Wait(context.Background(), "app", shouldWake, func(context.Context) error { return nil })
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	full := 0
	for _, e := range errs {
		if errors.Is(e, ErrQueueFull) {
			full++
		}
	}
	if full == 0 {
		t.Error("overflow past the waiter cap should return ErrQueueFull (→ 503)")
	}
}

func TestWakeGateRespectsCallerCancel(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shouldWake := func() bool { return true }
	err := g.Wait(ctx, "app", shouldWake, func(context.Context) error {
		time.Sleep(time.Second)
		return nil
	})
	if err == nil {
		t.Error("a cancelled caller should stop waiting")
	}
}

func TestWakeGateLeaderSkipsEnsureWhenShouldWakeIsFalse(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	calls := 0
	shouldWake := func() bool { return false }
	ensure := func(ctx context.Context) error { calls++; return nil }
	if err := g.Wait(context.Background(), "app", shouldWake, ensure); err != nil {
		t.Fatalf("Wait err = %v", err)
	}
	if calls != 0 {
		t.Errorf("shouldWake=false should skip ensure entirely, got %d calls", calls)
	}
}

// --- spec §6.2 invariants, gateway-side (Tier3 #9) -------------------

// TestInvariant1_CapPlusOneReturnsQueueFull mirrors §6.2-1 at the gateway
// layer: at cap+1 concurrent wake requests for a parked app, the (cap+1)th
// caller must see ErrQueueFull (the gateway's analogue of "an app's
// instance count is never exceeded").
func TestInvariant1_CapPlusOneReturnsQueueFull(t *testing.T) {
	const cap = 4
	g := NewWakeGate(cap, 5*time.Second)
	shouldWake := func() bool { return true }
	block := make(chan struct{})

	// Leader blocks until block closes; we close it AFTER spawning all
	// followers so the ErrQueueFull counts reflect a steady-state race.
	go func() {
		_ = g.Wait(context.Background(), "app", shouldWake, func(context.Context) error {
			<-block
			return nil
		})
	}()
	for g.InflightWaiters("app") < 1 {
		time.Sleep(time.Millisecond)
	}
	errs := make([]error, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.Wait(context.Background(), "app", shouldWake,
				func(context.Context) error { return nil })
		}(i)
	}
	// Give the followers time to all reach the gate and overflow.
	time.Sleep(50 * time.Millisecond)
	close(block) // let the leader finish so all queued followers can return
	wg.Wait()
	full := 0
	for _, e := range errs {
		if errors.Is(e, ErrQueueFull) {
			full++
		}
	}
	if full == 0 {
		t.Errorf("with cap=4 and 20 concurrent followers, at least one must be ErrQueueFull (got 0)")
	}
}

// TestInvariant4_InflightZeroAfterDrain mirrors §6.2-4 ("a parked app
// consumes zero resident RAM"): a completed and fully-drained gate has
// InflightWaiters == 0, so an idle reaper or capacity tracker can correctly
// observe "no live wakes" for the app.
func TestInvariant4_InflightZeroAfterDrain(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	shouldWake := func() bool { return true }
	const fans = 50
	var wg sync.WaitGroup
	for i := 0; i < fans; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Wait(context.Background(), "app", shouldWake,
				func(context.Context) error { return nil }); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := g.InflightWaiters("app"); got != 0 {
		t.Errorf("inflight after drain = %d, want 0 (invariant §6.2-4, gateway-side)", got)
	}
}

// TestInvariant5_NoSecondWakeAfterShouldWakeFalse is the §6.2-5 corollary
// at the gateway seam: when the leader's shouldWake=false short-circuit
// finishes, a follow-on caller observing the Backend as already-ready must
// NOT trigger a second ensure.
func TestInvariant5_NoSecondWakeAfterShouldWakeFalse(t *testing.T) {
	g := NewWakeGate(512, 5*time.Second)
	shouldWake := func() bool { return false }
	var calls int32
	ensure := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Wait(context.Background(), "app", shouldWake, ensure); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("ensure ran %d times; shouldWake=false must short-circuit all 25 → 0", got)
	}
}

// TestLimiterForget verifies Forget returns the bucket to a clean burst state.
// Origin: main — TestLimiterForget complements TestLimiterForgetAll above by
// pinning the single-key Forget semantics.
func TestLimiterForget(t *testing.T) {
	l := NewLimiter()
	if !l.Allow("app", api.PlanPro) {
		t.Fatal("first allow should succeed")
	}
	// Forget must drop the bucket so the next Allow is again a fresh burst.
	l.Forget("app")
	// Drain the burst fully to confirm Forget produced a brand-new bucket.
	limits := api.MustLimitsFor(api.PlanPro)
	consumed := 0
	for i := 0; i < limits.RateLimitBurst+5; i++ {
		if l.Allow("app", api.PlanPro) {
			consumed++
		}
	}
	if consumed != limits.RateLimitBurst {
		t.Errorf("after Forget, burst allowed = %d, want %d", consumed, limits.RateLimitBurst)
	}
	// Forget on unknown id is a no-op.
	l.Forget("never-existed")
}
