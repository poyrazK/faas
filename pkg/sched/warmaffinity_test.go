// warmaffinity_test.go — table-driven tests for pkg/sched.WarmAffinity
// (placement scheduler PR, ADR-025).
//
// The cache is the sticky-warm affinity hint the engine reads before
// calling ChoosePlacement. The tests pin three contracts:
//
//   1. RecordWake / LastWarmNode round-trip.
//   2. TTL eviction (lazy on read).
//   3. Forget removes an entry; concurrent RecordWake is race-safe.

package sched_test

import (
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// fakeClock is a deterministic time source for the TTL test. Tests
// advance the cursor via advance() rather than relying on time.Sleep,
// so the suite runs in milliseconds and never flakes on slow CI.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) nowFn() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newFakeClock(t *time.Time) *fakeClock {
	if t == nil {
		zero := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		t = &zero
	}
	return &fakeClock{now: *t}
}

func TestWarmAffinity_RoundTrip(t *testing.T) {
	w := sched.NewWarmAffinity(time.Minute)
	w.RecordWake("app-A", "node-X")
	got, found := w.LastWarmNode("app-A")
	if !found {
		t.Fatalf("LastWarmNode(app-A): found=false after RecordWake, want true")
	}
	if got != "node-X" {
		t.Errorf("LastWarmNode(app-A) = %q, want %q", got, "node-X")
	}
	if w.Len() != 1 {
		t.Errorf("Len() = %d, want 1", w.Len())
	}
}

func TestWarmAffinity_TTLExpiry(t *testing.T) {
	clk := newFakeClock(nil)
	w := sched.NewWarmAffinity(30 * time.Minute)
	w.SetClock(clk.nowFn)

	w.RecordWake("app-A", "node-X")
	if _, found := w.LastWarmNode("app-A"); !found {
		t.Fatalf("immediate LastWarmNode must find entry")
	}

	// Advance just past the TTL — entry should be evicted and found
	// should drop to false. Lazy eviction: the entry sits in the map
	// until the next read.
	clk.advance(31 * time.Minute)
	if _, found := w.LastWarmNode("app-A"); found {
		t.Errorf("LastWarmNode after TTL: found=true, want false (entry should be evicted)")
	}
	if w.Len() != 0 {
		t.Errorf("Len() after expiry = %d, want 0 (lazy eviction)", w.Len())
	}
}

func TestWarmAffinity_DefaultTTL(t *testing.T) {
	// Zero TTL falls back to api.WarmAffinityTTL (CLAUDE.md single
	// source of truth). Verifying the resolver keeps placement_test.go
	// honest if a future PR changes the default in pkg/api/limits.go.
	w := sched.NewWarmAffinity(0)
	if (*w.Ttl()) != api.WarmAffinityTTL {
		t.Errorf("NewWarmAffinity(0).ttl = %v, want %v (api.WarmAffinityTTL)", (*w.Ttl()), api.WarmAffinityTTL)
	}
}

func TestWarmAffinity_Forget(t *testing.T) {
	w := sched.NewWarmAffinity(time.Minute)
	w.RecordWake("app-A", "node-X")
	w.Forget("app-A")
	if _, found := w.LastWarmNode("app-A"); found {
		t.Errorf("LastWarmNode after Forget: found=true, want false")
	}
	// Forget on a missing app is idempotent (no panic).
	w.Forget("app-never-recorded")
}

func TestWarmAffinity_NilSafety(t *testing.T) {
	// All methods must tolerate a nil receiver so the engine's
	// optional warm-affinity wiring never panics on a missed
	// production wiring. A nil WarmAffinity is a no-op hint.
	var w *sched.WarmAffinity
	w.RecordWake("app-A", "node-X")
	if got, found := w.LastWarmNode("app-A"); found || got != "" {
		t.Errorf("nil.LastWarmNode = (%q, %v), want (\"\", false)", got, found)
	}
	w.Forget("app-A")
	if w.Len() != 0 {
		t.Errorf("nil.Len() = %d, want 0", w.Len())
	}
}

func TestWarmAffinity_RecordWakeRejectsEmpty(t *testing.T) {
	w := sched.NewWarmAffinity(time.Minute)
	w.RecordWake("", "node-X")
	w.RecordWake("app-A", "")
	if w.Len() != 0 {
		t.Errorf("Len() = %d after empty RecordWake, want 0", w.Len())
	}
}

func TestWarmAffinity_ConcurrentRecord(t *testing.T) {
	// Race detector probe (run with -race): N goroutines hammer
	// RecordWake for distinct apps. LastWarmNode must see a
	// consistent map (no nil-deref, no torn writes).
	w := sched.NewWarmAffinity(time.Minute)
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			appID := "app-" + string(rune('A'+i%26)) + "-" + string(rune('0'+i%10))
			w.RecordWake(appID, "node-X")
		}()
	}
	wg.Wait()
	if w.Len() == 0 {
		t.Errorf("Len() = 0 after concurrent RecordWake, want >0")
	}
}
