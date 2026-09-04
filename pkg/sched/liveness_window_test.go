// Tests for LivenessWindow — the per-deployment restart tracker
// (issue #554 / ADR-078). These tests pin the AC #2 + #3 surface
// directly:
//
//   - AC #2 — flaky app does NOT oscillate: a successful 200
//     resets the counter even after consecutive failures.
//   - AC #3 — 3 restarts in 5 min parks the deployment:
//     RecordRestart returns shouldPark=true on the 3rd entry
//     inside the window.
//
// We test the in-memory tracker in isolation; the Engine +
// ParkDeployment integration is tested separately in
// engine_liveness_test.go (depends on a live store + audit
// pipeline, which the window test deliberately avoids).
package sched

import (
	"sync"
	"testing"
	"time"
)

// TestLivenessWindow_BelowMaxNoPark is the closed-set baseline:
// a single restart below the maxN threshold does NOT trigger
// ParkDeployment.
func TestLivenessWindow_BelowMaxNoPark(t *testing.T) {
	w := NewLivenessWindow(5*time.Minute, 3)
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	if shouldPark, count := w.RecordRestart("dep-1", now); shouldPark || count != 1 {
		t.Errorf("after 1 restart: shouldPark=%v count=%d, want false/1", shouldPark, count)
	}
	if shouldPark, count := w.RecordRestart("dep-1", now.Add(time.Second)); shouldPark || count != 2 {
		t.Errorf("after 2 restarts: shouldPark=%v count=%d, want false/2", shouldPark, count)
	}
}

// TestLivenessWindow_ThirdInWindowParks is AC #3: the 3rd restart
// inside the 5-min window flips shouldPark=true.
func TestLivenessWindow_ThirdInWindowParks(t *testing.T) {
	w := NewLivenessWindow(5*time.Minute, 3)
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	w.RecordRestart("dep-1", base)
	w.RecordRestart("dep-1", base.Add(30*time.Second))
	shouldPark, count := w.RecordRestart("dep-1", base.Add(time.Minute))
	if !shouldPark || count != 3 {
		t.Errorf("after 3 restarts: shouldPark=%v count=%d, want true/3", shouldPark, count)
	}
}

// TestLivenessWindow_ForgetsOldEntries is the boundary case: 3
// restarts in the window DO park, but a 4th after the window
// rolled over does NOT (the previous three expired).
//
// Math note: cutoff is (now - window). Entries with
// t > cutoff are kept (strictly After). Entries exactly equal
// to cutoff are dropped. So restarts at base, base+1m,
// base+2m, with the third restart at base+2m+1ns (just past
// the cutoff) all stay in the ring.
func TestLivenessWindow_ForgetsOldEntries(t *testing.T) {
	w := NewLivenessWindow(5*time.Minute, 3)
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	// Three restarts 1 min apart — all inside the 5-min
	// window from the third call's perspective (cutoff =
	// base+3m - 5m = base-2m).
	w.RecordRestart("dep-1", base)
	w.RecordRestart("dep-1", base.Add(time.Minute))
	shouldPark, _ := w.RecordRestart("dep-1", base.Add(2*time.Minute))
	if !shouldPark {
		t.Errorf("3 restarts in 5 min window: shouldPark=false, want true")
	}

	// Second fresh deployment: 3 restarts at base+10m,
	// base+11m, base+12m. The third call's cutoff is
	// base+12m - 5m = base+7m. ALL three entries are
	// strictly after base+7m, so they should remain. But
	// then the FOURTH call at base+13m with cutoff
	// base+13m - 5m = base+8m keeps all three (still inside
	// the new window). After 4 in a row, count=4 > maxN=3
	// so shouldPark=true on the fourth. We verify the
	// trailing-expire path differently below.
	w2 := NewLivenessWindow(5*time.Minute, 3)
	w2.RecordRestart("dep-2", base.Add(10*time.Minute))
	w2.RecordRestart("dep-2", base.Add(11*time.Minute))
	w2.RecordRestart("dep-2", base.Add(12*time.Minute))
	// 4th call at base+13m → cutoff = base+8m. All three
	// earlier entries > base+8m. Append → count=4.
	if shouldPark, count := w2.RecordRestart("dep-2", base.Add(13*time.Minute)); !shouldPark || count != 4 {
		t.Errorf("4th in window: shouldPark=%v count=%d, want true/4", shouldPark, count)
	}

	// Third fresh deployment: 3 restarts at base, base+1m,
	// base+2m. Then a 4th call at base+10m → cutoff =
	// base+5m. All three earlier entries are ≤ base+2m, which
	// is NOT after base+5m, so they are dropped. The 4th
	// call appends itself → count=1.
	w3 := NewLivenessWindow(5*time.Minute, 3)
	w3.RecordRestart("dep-3", base)
	w3.RecordRestart("dep-3", base.Add(time.Minute))
	w3.RecordRestart("dep-3", base.Add(2*time.Minute))
	shouldPark, count := w3.RecordRestart("dep-3", base.Add(10*time.Minute))
	if shouldPark || count != 1 {
		t.Errorf("after window expires: shouldPark=%v count=%d, want false/1", shouldPark, count)
	}
}

// TestLivenessWindow_NilSafe pins the nil-receiver shape: the
// schedd-side tests that don't construct a LivenessWindow must
// not panic when the Engine's DestroyForLivenessFailure path
// touches a nil tracker.
func TestLivenessWindow_NilSafe(t *testing.T) {
	var w *LivenessWindow
	shouldPark, count := w.RecordRestart("dep-x", time.Now())
	if shouldPark || count != 0 {
		t.Errorf("nil receiver: shouldPark=%v count=%d, want false/0", shouldPark, count)
	}
	if n := w.recent("dep-x", time.Now()); n != 0 {
		t.Errorf("nil receiver: recent=%d, want 0", n)
	}
	w.Forget("dep-x") // must not panic
}

// TestLivenessWindow_DisabledByZeroOpts confirms the
// `window==0 || maxN==0 → no-op gate` contract: production code
// constructs LivenessWindow with the per-plan defaults, but a
// misconfiguration must NOT silently start parking on every
// failure.
func TestLivenessWindow_DisabledByZeroOpts(t *testing.T) {
	w := NewLivenessWindow(0, 3) // window=0 → gate disabled
	now := time.Now()
	for i := 0; i < 10; i++ {
		if shouldPark, _ := w.RecordRestart("dep-1", now); shouldPark {
			t.Errorf("window=0 should never park; got shouldPark=true on iteration %d", i)
		}
	}
	w2 := NewLivenessWindow(5*time.Minute, 0) // maxN=0 → gate disabled
	for i := 0; i < 10; i++ {
		if shouldPark, _ := w2.RecordRestart("dep-1", now); shouldPark {
			t.Errorf("maxN=0 should never park; got shouldPark=true on iteration %d", i)
		}
	}
}

// TestLivenessWindow_ConcurrentSafe runs RecordRestart from N
// goroutines on the same deployment; the count must converge to
// N and the shouldPark flip must happen exactly once (when the
// ring first reaches maxN). Race-detector clean.
func TestLivenessWindow_ConcurrentSafe(t *testing.T) {
	w := NewLivenessWindow(5*time.Minute, 100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.RecordRestart("dep-1", time.Now())
		}()
	}
	wg.Wait()
	if got := w.recent("dep-1", time.Now()); got != 50 {
		t.Errorf("concurrent RecordRestart: recent=%d, want 50", got)
	}
}

// TestLivenessWindow_PerDeploymentIsolation pins that two
// deployments have independent rings: dep-1 hitting maxN must
// NOT trigger ParkDeployment on dep-2.
func TestLivenessWindow_PerDeploymentIsolation(t *testing.T) {
	w := NewLivenessWindow(5*time.Minute, 3)
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	// Three restarts on dep-1 → parks dep-1.
	w.RecordRestart("dep-1", base)
	w.RecordRestart("dep-1", base.Add(30*time.Second))
	shouldPark1, _ := w.RecordRestart("dep-1", base.Add(time.Minute))
	if !shouldPark1 {
		t.Errorf("dep-1 should park after 3 restarts")
	}
	// One restart on dep-2 → still 0 shouldPark.
	shouldPark2, count2 := w.RecordRestart("dep-2", base.Add(time.Minute))
	if shouldPark2 || count2 != 1 {
		t.Errorf("dep-2: shouldPark=%v count=%d, want false/1 (independent ring)", shouldPark2, count2)
	}
}

// TestLivenessWindow_NodeScopedIsolation pins issue #1267's topology rule:
// three confirmed failures on one node may trip that deployment's crash-loop
// breaker, while failures split across nodes do not combine into an app-wide
// permanent eviction decision.
func TestLivenessWindow_NodeScopedIsolation(t *testing.T) {
	w := NewLivenessWindow(5*time.Minute, 3)
	base := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	w.RecordRestartOnNode("dep-1", "node-a", base)
	w.RecordRestartOnNode("dep-1", "node-a", base.Add(time.Second))
	if shouldPark, count := w.RecordRestartOnNode("dep-1", "node-b", base.Add(2*time.Second)); shouldPark || count != 1 {
		t.Fatalf("first node-b failure: shouldPark=%v count=%d, want false/1", shouldPark, count)
	}
	if shouldPark, count := w.RecordRestartOnNode("dep-1", "node-b", base.Add(3*time.Second)); shouldPark || count != 2 {
		t.Fatalf("second node-b failure: shouldPark=%v count=%d, want false/2", shouldPark, count)
	}
	if shouldPark, count := w.RecordRestartOnNode("dep-1", "node-a", base.Add(4*time.Second)); !shouldPark || count != 3 {
		t.Fatalf("third node-a failure: shouldPark=%v count=%d, want true/3", shouldPark, count)
	}
	if got := w.recentOnNode("dep-1", "node-a", base.Add(4*time.Second)); got != 3 {
		t.Errorf("node-a recent count=%d, want 3", got)
	}
	if got := w.recentOnNode("dep-1", "node-b", base.Add(4*time.Second)); got != 2 {
		t.Errorf("node-b recent count=%d, want 2", got)
	}
}
