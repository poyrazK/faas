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
