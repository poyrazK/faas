package meter_test

import (
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestLoop_Health_NeverFired — a brand-new Loop with no ticks has
// Healthy=false and lists every wired timer in Stale. Pins the
// "first-tick warm-up" semantics documented in health.go: until the
// first tick lands the daemon is NOT healthy by spec.
func TestLoop_Health_NeverFired(t *testing.T) {
	t.Parallel()
	loop, _ := newHealthFixture(t, true)
	status := loop.Health(time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	if status.Healthy {
		t.Errorf("Healthy = true on fresh Loop, want false (no ticks yet)")
	}
	want := []string{"dunning", "quota", "sample", "stripe"}
	got := append([]string(nil), status.Stale...)
	sort.Strings(got)
	if !equalStrings(got, want) {
		t.Errorf("Stale = %v, want %v", got, want)
	}
	for name, ts := range status.Ticks {
		if ts != "never" {
			t.Errorf("Ticks[%q] = %q, want \"never\"", name, ts)
		}
	}
}

// TestLoop_Health_NeverFiredNoDunning — regression guard for the fix
// that mirrors Loop.Run's conditional dunning wiring (loop.go:64-69):
// when l.dunning is nil, "dunning" must not appear in Stale, or any
// test (or production misconfig) without a dunning timer permanently
// reports 503.
func TestLoop_Health_NeverFiredNoDunning(t *testing.T) {
	t.Parallel()
	loop, _ := newHealthFixture(t, false)
	status := loop.Health(time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	if status.Healthy {
		t.Errorf("Healthy = true on fresh Loop, want false")
	}
	for _, name := range status.Stale {
		if name == "dunning" {
			t.Errorf("Stale contains \"dunning\" but loop was constructed without one")
		}
	}
	// And the Ticks map reflects the same — no dunning key.
	if _, ok := status.Ticks["dunning"]; ok {
		t.Errorf("Ticks contains \"dunning\" but loop was constructed without one")
	}
}

// TestLoop_Health_FreshAll — once each timer has fired at least once,
// the daemon reports Healthy and every Ticks entry is a real RFC-3339
// timestamp (not "never"). Loop.runTicks stamps lastTick from
// time.Now(), so the "now" argument to Health() must be the real wall
// clock — picked right after the brief run returns so every tick is
// well inside the 60 ms (3 × 20 ms) threshold.
func TestLoop_Health_FreshAll(t *testing.T) {
	t.Parallel()
	loop, _ := runLoopBriefForHealth(t, state.NewMemStore())
	status := loop.Health(time.Now())
	if !status.Healthy {
		t.Errorf("Healthy = false on freshly-ticked Loop (Stale=%v, Ticks=%v)",
			status.Stale, status.Ticks)
	}
	if len(status.Stale) != 0 {
		t.Errorf("Stale = %v, want empty", status.Stale)
	}
	for name, ts := range status.Ticks {
		if ts == "never" {
			t.Errorf("Ticks[%q] = \"never\" after the loop ran", name)
		}
		// RFC-3339 sanity.
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Errorf("Ticks[%q] = %q is not RFC-3339: %v", name, ts, err)
		}
	}
}

// TestLoop_Health_StaleAfterMultiplier — once `now` exceeds
// StaleAfterMultiplier × SampleInterval since the last sample tick,
// that timer is reported in Stale and Healthy flips to false. Pins the
// spec §14 M7 wording: 3 × 60s = 3 min, with the configured
// SampleInterval=20ms giving a 60ms threshold for this test.
func TestLoop_Health_StaleAfterMultiplier(t *testing.T) {
	t.Parallel()
	loop, _ := runLoopBriefForHealth(t, state.NewMemStore())
	// Loop.runTicks stamps lastTick from time.Now(); the brief run
	// lasted ~150 ms. Pick `now` = (loop end + 1 second) — well past
	// the 60 ms threshold for every wired timer.
	now := time.Now().Add(1 * time.Second)
	status := loop.Health(now)
	if status.Healthy {
		t.Errorf("Healthy = true when now is far past every timer's threshold (Ticks=%v)",
			status.Ticks)
	}
	// Every wired timer must be stale.
	for _, name := range []string{"sample", "quota", "stripe"} {
		found := false
		for _, n := range status.Stale {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Stale missing %q (have %v)", name, status.Stale)
		}
	}
}

// TestLoop_Health_RecordsLastTick — LastTick(name) returns the wall-
// clock time of the named tick's last successful run; ok==true, the
// timestamp is within the brief-run window, and the recorded value is
// a recent stamp (no older than 200 ms before `time.Now()`). Pins the
// write-path: a regression that drops the `l.recordTick(name, start)`
// call from runTicks would leave LastTick returning the zero value
// forever, and this test would catch it.
func TestLoop_Health_RecordsLastTick(t *testing.T) {
	t.Parallel()
	loop, _ := runLoopBriefForHealth(t, state.NewMemStore())
	// The brief run sleeps 150 ms then cancels; the last tick for any
	// timer landed within that window. `now` is captured after the
	// run returns — the recorded stamp must be ≤ 200 ms in the past
	// (150 ms brief-run + slack for goroutine scheduling).
	now := time.Now()
	for _, name := range []string{"sample", "quota", "stripe"} {
		ts, ok := loop.LastTick(name)
		if !ok {
			t.Errorf("LastTick(%q) ok = false after runLoopBrief", name)
			continue
		}
		if ts.IsZero() {
			t.Errorf("LastTick(%q) is zero value (recordTick never fired)", name)
		}
		if age := now.Sub(ts); age < 0 || age > 200*time.Millisecond {
			t.Errorf("LastTick(%q) = %v, age = %v, want 0..200ms",
				name, ts, age)
		}
	}
	// An unknown tick name returns ok=false; sanity-check the seam.
	if _, ok := loop.LastTick("nonexistent"); ok {
		t.Errorf("LastTick(\"nonexistent\") ok = true, want false")
	}
}

// TestLoop_Health_MultiplierMatchesSpec — pins the §14 M7 multiplier
// against the production-default SampleInterval so a default change
// can't silently break the contract.
func TestLoop_Health_MultiplierMatchesSpec(t *testing.T) {
	t.Parallel()
	cfg := &meter.Config{}
	cfg.Defaults()
	if meter.StaleAfterMultiplier != 3 {
		t.Errorf("StaleAfterMultiplier = %d, want 3 (spec §14 M7)", meter.StaleAfterMultiplier)
	}
	if cfg.SampleInterval != 60*time.Second {
		t.Fatalf("SampleInterval default = %v, want 60s", cfg.SampleInterval)
	}
	wantThreshold := meter.StaleAfterMultiplier * cfg.SampleInterval
	if wantThreshold != 3*time.Minute {
		t.Errorf("threshold = %v, want 3m", wantThreshold)
	}
}

// --- helpers ---

// newHealthFixture builds a Loop with sub-second intervals. Mirrors
// the runLoopBrief shape but doesn't drive the loop — pure-construction
// fixture for the "never fired" / "no dunning" tests.
func newHealthFixture(t *testing.T, withDunning bool) (*meter.Loop, *meter.Config) {
	t.Helper()
	cfg := &meter.Config{}
	cfg.Defaults()
	cfg.SampleInterval = 1 * time.Second
	cfg.QuotaInterval = 1 * time.Second
	cfg.StripeInterval = 60 * time.Second
	cfg.DunningInterval = 60 * time.Second

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	var dunning *meter.Dunning
	if withDunning {
		dunning = meter.NewDunning(meter.DunningParams{
			Store:    state.NewMemStore(),
			Parker:   &fakeParker{},
			Interval: cfg.DunningInterval,
			Now:      func() time.Time { return now },
			Log:      discardLog(),
		})
	}
	ops := wire.NewOpsMetrics("meter_test_health")
	loop := meter.NewLoop(
		state.NewMemStore(),
		&fakeParker{},
		nil,
		&fakeNotifier{},
		dunning,
		nil, // residency — nil; the gauge emit is exercised by residency_test.go
		func() time.Time { return now },
		discardLog(),
		cfg,
		ops,
	)
	return loop, cfg
}

// runLoopBriefForHealth drives the loop briefly and returns it for the
// "freshly-ticked" tests. Wraps runLoopBrief with the fakeParker /
// fakeNotifier collaborators needed for runLoopBrief's signature.
func runLoopBriefForHealth(t *testing.T, store state.Store) (*meter.Loop, *wire.OpsMetrics) {
	t.Helper()
	return runLoopBrief(t, store, nil)
}

// equalStrings is the go <= 1.21 safe equivalent of slices.Equal on
// sorted inputs.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
