package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"sync"
	"testing"
	"time"
)

// frozenClockNow returns a fixed-time clock seam for the aggregator
// tests. The IncAtCapacity and PressuredApps entry points accept
// the time as a parameter so the seam is at the call site —
// the aggregator's internal clock is only used as a guard
// against future code paths that want to call the package
// without a time argument.
func frozenClockNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestPressureAggregator_ThresholdCrossing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))

	const appID = "app-1"
	// 4 events under the threshold — must NOT show as pressured.
	for i := 0; i < 4; i++ {
		agg.IncAtCapacity(appID, now.Add(time.Duration(i)*time.Second))
	}
	got := agg.PressuredApps(5, now.Add(4*time.Second))
	if len(got) != 0 {
		t.Fatalf("expected no pressured apps at 4 events, got %v", got)
	}

	// 5th event — trips the threshold.
	agg.IncAtCapacity(appID, now.Add(5*time.Second))
	got = agg.PressuredApps(5, now.Add(5*time.Second))
	if len(got) != 1 || got[0] != appID {
		t.Fatalf("expected [%s] at 5 events, got %v", appID, got)
	}
}

func TestPressureAggregator_PerAppIsolation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))

	for i := 0; i < 5; i++ {
		agg.IncAtCapacity("app-hot", now.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < 2; i++ {
		agg.IncAtCapacity("app-cold", now.Add(time.Duration(i)*time.Second))
	}
	got := agg.PressuredApps(5, now.Add(5*time.Second))
	if len(got) != 1 || got[0] != "app-hot" {
		t.Fatalf("expected [app-hot], got %v", got)
	}
}

func TestPressureAggregator_WindowEdgePruning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))

	// 5 events at now-59s (inside the 60s window) — pressured.
	for i := 0; i < 5; i++ {
		agg.IncAtCapacity("app", now.Add(-59*time.Second+time.Duration(i)*time.Second))
	}
	got := agg.PressuredApps(5, now)
	if len(got) != 1 {
		t.Fatalf("expected 1 pressured app at the window edge, got %v", got)
	}

	// Walk forward 2s — the 5 events are now at -61s, outside the window.
	got = agg.PressuredApps(5, now.Add(2*time.Second))
	if len(got) != 0 {
		t.Fatalf("expected 0 pressured apps after window elapse, got %v", got)
	}
}

func TestPressureAggregator_ResetClearsApp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))

	for i := 0; i < 5; i++ {
		agg.IncAtCapacity("app", now.Add(time.Duration(i)*time.Second))
	}
	agg.Reset("app")
	got := agg.PressuredApps(5, now.Add(5*time.Second))
	if len(got) != 0 {
		t.Fatalf("expected 0 pressured apps after Reset, got %v", got)
	}
}

func TestPressureAggregator_SortedOutput(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))

	for _, id := range []string{"zeta", "alpha", "mu", "beta"} {
		for i := 0; i < 5; i++ {
			agg.IncAtCapacity(id, now.Add(time.Duration(i)*time.Second))
		}
	}
	got := agg.PressuredApps(5, now.Add(5*time.Second))
	want := []string{"alpha", "beta", "mu", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("expected %v at index %d, got %v", w, i, got[i])
		}
	}
}

func TestPressureAggregator_NilSafe(t *testing.T) {
	t.Parallel()
	var agg *sched.PressureAggregator
	agg.IncAtCapacity("app", time.Now())
	if got := agg.Count("app", time.Now()); got != 0 {
		t.Fatalf("expected 0 from nil Count, got %d", got)
	}
	if got := agg.PressuredApps(5, time.Now()); got != nil {
		t.Fatalf("expected nil from nil PressuredApps, got %v", got)
	}
	agg.Reset("app")
	agg.ResetAll()
}

func TestPressureAggregator_ConcurrentIncAtCapacity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100_000, frozenClockNow(now))

	const goroutines = 32
	const perGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				agg.IncAtCapacity("shared-app", now)
			}
		}()
	}
	wg.Wait()

	got := agg.Count("shared-app", now)
	if got != goroutines*perGoroutine {
		t.Fatalf("expected %d events under -race, got %d", goroutines*perGoroutine, got)
	}
}

func TestPressureAggregator_MaxEventsPerAppCap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	const cap = 5
	agg := sched.NewPressureAggregatorForTest(60*time.Second, cap, frozenClockNow(now))

	for i := 0; i < 10; i++ {
		agg.IncAtCapacity("app", now.Add(time.Duration(i)*time.Millisecond))
	}
	// Cap trims the oldest; the 5 newest survive.
	got := agg.Count("app", now.Add(10*time.Millisecond))
	if got != cap {
		t.Fatalf("expected %d events after cap, got %d", cap, got)
	}
}

func TestPressureAggregator_ResetAll(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	agg := sched.NewPressureAggregatorForTest(60*time.Second, 100, frozenClockNow(now))

	for _, id := range []string{"a", "b", "c"} {
		for i := 0; i < 5; i++ {
			agg.IncAtCapacity(id, now.Add(time.Duration(i)*time.Second))
		}
	}
	agg.ResetAll()
	got := agg.PressuredApps(5, now.Add(5*time.Second))
	if len(got) != 0 {
		t.Fatalf("expected 0 pressured apps after ResetAll, got %v", got)
	}
}

func TestPressureAggregator_NewPressureAggregatorForTest_Panics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		window          time.Duration
		maxEventsPerApp int
	}{
		{"zero window", 0, 100},
		{"negative window", -1 * time.Second, 100},
		{"zero maxEvents", 60 * time.Second, 0},
		{"negative maxEvents", 60 * time.Second, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for case %q", tc.name)
				}
			}()
			sched.NewPressureAggregatorForTest(tc.window, tc.maxEventsPerApp, time.Now)
		})
	}
}

func TestPressureAggregator_NewPressureAggregatorForTest_NilNowPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for nil now")
		}
	}()
	sched.NewPressureAggregatorForTest(60*time.Second, 100, nil)
}
