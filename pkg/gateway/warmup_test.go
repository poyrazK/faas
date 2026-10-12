// Tests for the standby warm-up scraper (Tier A8 / ADR-083).

package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeProber records probe calls and returns scripted errors.
type fakeProber struct {
	mu    sync.Mutex
	calls []string
	errs  map[string]error // slug → err to return
	count atomic.Int64
}

func (f *fakeProber) Probe(_ context.Context, slug string) error {
	f.mu.Lock()
	f.calls = append(f.calls, slug)
	f.mu.Unlock()
	f.count.Add(1)
	if f.errs == nil {
		return nil
	}
	return f.errs[slug]
}

func (f *fakeProber) CallCount() int64 { return f.count.Load() }

// Keep these in-process interval tests independent of host scheduling pauses.
func TestWarmupLoop_RunsOnInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProber{}
		loop := &WarmupLoop{
			Prober:       p,
			Interval:     10 * time.Millisecond,
			ProbeTimeout: 5 * time.Millisecond,
			Slugs:        func() []string { return []string{"app-a", "app-b"} },
		}
		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
		defer cancel()
		if err := loop.Run(ctx); err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run returned %v, want DeadlineExceeded", err)
		}
		// Initial probe plus five interval ticks inside 55ms, each with two apps.
		got := p.CallCount()
		if got != 12 {
			t.Errorf("expected 12 probes across initial and interval ticks, got %d", got)
		}
	})
}

func TestWarmupLoop_ProbeFailuresSwallowed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProber{errs: map[string]error{
			"app-a": errors.New("503"),
		}}
		var seen []string
		var mu sync.Mutex
		loop := &WarmupLoop{
			Prober:       p,
			Interval:     10 * time.Millisecond,
			ProbeTimeout: 5 * time.Millisecond,
			Slugs:        func() []string { return []string{"app-a"} },
			OnError: func(slug string, err error) {
				mu.Lock()
				seen = append(seen, slug)
				mu.Unlock()
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel()
		if err := loop.Run(ctx); err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run returned %v, want DeadlineExceeded", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 4 {
			t.Errorf("expected four app-a probe errors, got %d", len(seen))
		}
		for _, s := range seen {
			if s != "app-a" {
				t.Errorf("OnError called with unexpected slug %q", s)
			}
		}
	})
}

func TestWarmupLoop_DefaultsFromAPI(t *testing.T) {
	// When Interval and ProbeTimeout are zero, the loop
	// resolves them from pkg/api. HAStandbyWarmupIntervalMS
	// = 500, HAFailoverProbeTimeoutMS = 500. The test asserts
	// that the defaults are non-zero so a forgotten wire-up
	// doesn't silently break the loop (the loop would tick
	// every 0 ns).
	if api.HAStandbyWarmupIntervalMS <= 0 {
		t.Fatalf("api.HAStandbyWarmupIntervalMS must be > 0")
	}
	if api.HAFailoverProbeTimeoutMS <= 0 {
		t.Fatalf("api.HAFailoverProbeTimeoutMS must be > 0")
	}
	loop := &WarmupLoop{Slugs: func() []string { return nil }}
	if loop.Interval != 0 || loop.ProbeTimeout != 0 {
		t.Fatalf("zero-value WarmupLoop expected")
	}
	// Don't actually Run() — the defaults would tick every
	// 500 ms for ~forever. The Run() method's first
	// statement sets the defaults; we verify by reading the
	// constant and trusting the assignment.
	if time.Duration(api.HAStandbyWarmupIntervalMS)*time.Millisecond != 500*time.Millisecond {
		t.Errorf("HAStandbyWarmupIntervalMS = %d, want 500", api.HAStandbyWarmupIntervalMS)
	}
	if time.Duration(api.HAFailoverProbeTimeoutMS)*time.Millisecond != 500*time.Millisecond {
		t.Errorf("HAFailoverProbeTimeoutMS = %d, want 500", api.HAFailoverProbeTimeoutMS)
	}
}

func TestWarmupLoop_CtxCancelExits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProber{}
		loop := &WarmupLoop{
			Prober:       p,
			Interval:     100 * time.Millisecond,
			ProbeTimeout: 5 * time.Millisecond,
			Slugs:        func() []string { return []string{"app-a"} },
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- loop.Run(ctx) }()
		time.Sleep(20 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run returned %v, want Canceled", err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Run did not exit within 100ms after cancel")
		}
	})
}

func TestWarmupLoop_NilSlugsIsNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakeProber{}
		loop := &WarmupLoop{
			Prober:       p,
			Interval:     10 * time.Millisecond,
			ProbeTimeout: 5 * time.Millisecond,
			// Slugs intentionally nil — the loop's Run() must
			// default to a no-op so PR-A's bare-metal fixture
			// doesn't have to wire one.
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		if err := loop.Run(ctx); err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run returned %v, want DeadlineExceeded", err)
		}
		if p.CallCount() != 0 {
			t.Errorf("nil Slugs must not probe, got %d calls", p.CallCount())
		}
	})
}

func TestWarmupLoop_ShrinkingSlugs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Slugs returns a shrinking list across ticks — the loop
		// must use the current list, handle an empty list, and stop
		// probing slugs that have been removed.
		p := &fakeProber{}
		var tickCount atomic.Int64
		loop := &WarmupLoop{
			Prober:       p,
			Interval:     10 * time.Millisecond,
			ProbeTimeout: 5 * time.Millisecond,
			Slugs: func() []string {
				n := tickCount.Add(1)
				switch n {
				case 1:
					return []string{"app-a", "app-b"}
				case 2:
					return []string{"app-a"} // app-b removed
				default:
					return nil // empty after that
				}
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel()
		if err := loop.Run(ctx); err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run returned %v, want DeadlineExceeded", err)
		}
		if got := tickCount.Load(); got != 4 {
			t.Fatalf("expected four slug-list reads, got %d", got)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		calls := make(map[string]int)
		for _, slug := range p.calls {
			calls[slug]++
		}
		if len(p.calls) != 3 || calls["app-a"] != 2 || calls["app-b"] != 1 {
			t.Errorf("unexpected probes across shrinking slugs: %v", p.calls)
		}
	})
}
