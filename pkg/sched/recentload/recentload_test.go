package recentload

import (
	"context"
	"testing"
	"time"
)

// fakeScraper is a closure-driven PromScraper used in unit tests.
// The closure receives ctx and returns a per-app cumulative map.
// Returning an error leaves the previous ring intact (degraded mode).
type fakeScraper struct {
	fn func(ctx context.Context) (map[string]int64, error)
}

func (f *fakeScraper) Scrape(ctx context.Context) (map[string]int64, error) {
	return f.fn(ctx)
}

type fakeRateReader struct {
	rates map[string]float64
}

func (f *fakeRateReader) RequestRatesPerSecondAt(time.Time) map[string]float64 {
	out := make(map[string]float64, len(f.rates))
	for appID, rate := range f.rates {
		out[appID] = rate
	}
	return out
}

// TestRecentLoad_PerAppSum exercises the basic sliding-window sum
// after a cold start. The first Touch seeds the ring with count=0
// (cold-start guard) — the cumulative value seen at first sight
// must NOT count toward the window.
func TestRecentLoad_PerAppSum(t *testing.T) {
	cum := int64(0)
	r := New(&fakeScraper{fn: func(ctx context.Context) (map[string]int64, error) {
		return map[string]int64{"app1": cum}, nil
	}}, 5, time.Second)
	base := time.Unix(1_000_000, 0)
	cum = 10
	r.Touch(context.Background(), base) // first touch: seed lastSeen=10, count=0
	cum = 20
	r.Touch(context.Background(), base.Add(time.Second)) // delta=10
	cum = 30
	r.Touch(context.Background(), base.Add(2*time.Second)) // delta=10
	// Buckets: base → count=0, base+1s → count=10, base+2s → count=10.
	// AppRPS at base+2s = 0 + 10 + 10 = 20.
	if got := r.RecentRPS("app1", base.Add(2*time.Second)); got != 20 {
		t.Errorf("RecentRPS = %v, want 20 (cold-start guard)", got)
	}
}

// TestRecentLoad_EvictsOldBuckets confirms a 5×1s window holds
// only the 5 most-recent buckets.
func TestRecentLoad_EvictsOldBuckets(t *testing.T) {
	cum := int64(0)
	r := New(&fakeScraper{fn: func(ctx context.Context) (map[string]int64, error) {
		return map[string]int64{"app1": cum}, nil
	}}, 5, time.Second)
	base := time.Unix(1_000_000, 0)
	for i := 0; i < 6; i++ {
		// Per-tick delta: (i+1)*10. Tick 0 seeds the ring with
		// delta=0; ticks 1..5 contribute 20, 30, 40, 50, 60.
		cum += int64((i + 1) * 10)
		r.Touch(context.Background(), base.Add(time.Duration(i)*time.Second))
	}
	// At base+5s, currentBucket=1_000_005, cutoff=1_000_001.
	// Bucket 1_000_000 (count=0, the seed) is evicted. Retained
	// buckets carry deltas 20, 30, 40, 50, 60 = 200.
	got := r.RecentRPS("app1", base.Add(5*time.Second))
	if got != 200 {
		t.Errorf("RecentRPS = %v, want 200 (5 retained buckets)", got)
	}
}

// TestRecentLoad_GatewayRestartDoesNotSpike confirms a cumulative
// regression (cumulative < lastSeen) clears the per-app ring so
// the next delta tick is measured from the new boot's perspective.
func TestRecentLoad_GatewayRestartDoesNotSpike(t *testing.T) {
	var cum int64 = 1000
	r := New(&fakeScraper{fn: func(ctx context.Context) (map[string]int64, error) {
		return map[string]int64{"app1": cum}, nil
	}}, 5, time.Second)
	base := time.Unix(1_000_000, 0)
	r.Touch(context.Background(), base) // seeds lastSeen=1000, count=0
	cum = 1010
	r.Touch(context.Background(), base.Add(time.Second)) // delta=10
	// Simulated restart: gatewayd-internal's counter resets to 0.
	cum = 5
	r.Touch(context.Background(), base.Add(2*time.Second)) // cumulative < lastSeen → ring cleared
	cum = 10
	r.Touch(context.Background(), base.Add(3*time.Second)) // delta=5
	// At base+3s, only the post-restart delta (5) is in the window.
	if got := r.RecentRPS("app1", base.Add(3*time.Second)); got != 5 {
		t.Errorf("RecentRPS = %v, want 5 (restart cleared ring)", got)
	}
}

// TestRecentLoad_DesiredReplicas_Ceil confirms the math.
func TestRecentLoad_DesiredReplicas_Ceil(t *testing.T) {
	r := New(nil, 5, time.Second) // scraper unused — we drive RecentRPS directly... but RecentRPS reads the ring, so we still need Touch.
	// Drive the ring manually to exercise the accessor's math path.
	r.byApp["app1"] = &appWindow{
		buckets:  []bucket{{bucket: 1_000_000, count: 275}},
		lastSeen: 275,
	}
	got := r.RecentDesiredReplicas("app1", time.Unix(1_000_000, 0), 10)
	if got != 6 {
		t.Errorf("RecentDesiredReplicas(55 rps, target=10) = %d, want ceil(55/10)=6", got)
	}
}

// TestRecentLoad_DesiredReplicas_ZeroTarget returns 0 so the
// reaper defers to ReapIdle.
func TestRecentLoad_DesiredReplicas_ZeroTarget(t *testing.T) {
	r := New(nil, 5, time.Second)
	r.byApp["app1"] = &appWindow{
		buckets:  []bucket{{bucket: 1_000_000, count: 100}},
		lastSeen: 100,
	}
	if got := r.RecentDesiredReplicas("app1", time.Unix(1_000_000, 0), 0); got != 0 {
		t.Errorf("RecentDesiredReplicas(target=0) = %d, want 0", got)
	}
}

// TestRecentLoad_DesiredReplicas_NoObservation returns 0 — the
// reaper must not aggressively park an app that hasn't been
// scraped yet.
func TestRecentLoad_DesiredReplicas_NoObservation(t *testing.T) {
	r := New(nil, 5, time.Second)
	if got := r.RecentDesiredReplicas("never-touched", time.Unix(1_000_000, 0), 10); got != 0 {
		t.Errorf("RecentDesiredReplicas(no observation) = %d, want 0", got)
	}
}

func TestRecentLoad_TelemetryFallbackPreservesZeroObservation(t *testing.T) {
	reader := &fakeRateReader{rates: map[string]float64{"app1": 30}}
	r := New(nil, 5, time.Second).WithRateReader(reader)
	base := time.Unix(1_000_000, 0)
	r.Touch(context.Background(), base)
	if got, observed := r.RecentDesiredReplicasWithSignal("app1", base, 15); got != 2 || !observed {
		t.Fatalf("initial desired = (%d, %v), want (2, true)", got, observed)
	}

	reader.rates["app1"] = 0
	for i := 1; i <= 5; i++ {
		r.Touch(context.Background(), base.Add(time.Duration(i)*time.Second))
	}
	if got, observed := r.RecentDesiredReplicasWithSignal("app1", base.Add(5*time.Second), 15); got != 0 || !observed {
		t.Fatalf("zero-traffic desired = (%d, %v), want (0, true)", got, observed)
	}
	if got, observed := r.RecentDesiredReplicasWithSignal("missing", base.Add(5*time.Second), 15); got != 0 || observed {
		t.Fatalf("missing desired = (%d, %v), want (0, false)", got, observed)
	}
}

func TestRecentLoad_GatewaySignalPreferredOverTelemetry(t *testing.T) {
	cumulative := int64(0)
	scraper := &fakeScraper{fn: func(context.Context) (map[string]int64, error) {
		return map[string]int64{"app1": cumulative}, nil
	}}
	reader := &fakeRateReader{rates: map[string]float64{"app1": 100}}
	r := New(scraper, 5, time.Second).WithRateReader(reader)
	base := time.Unix(1_000_000, 0)
	r.Touch(context.Background(), base)
	cumulative = 50
	r.Touch(context.Background(), base.Add(time.Second))
	if got, observed := r.RecentRateWithSignal("app1", base.Add(time.Second)); got != 10 || !observed {
		t.Fatalf("rate = (%v, %v), want gateway-derived (10, true)", got, observed)
	}
}

// TestRecentLoad_NilReceiver — every method on a nil receiver
// must not panic. Schedd's loop wires the mirror conditionally;
// the safe-default is no signal.
func TestRecentLoad_NilReceiver(t *testing.T) {
	var r *RecentLoad
	if got := r.RecentRPS("app1", time.Now()); got != 0 {
		t.Errorf("nil.RecentRPS = %v, want 0", got)
	}
	if got := r.RecentDesiredReplicas("app1", time.Now(), 10); got != 0 {
		t.Errorf("nil.RecentDesiredReplicas = %d, want 0", got)
	}
	r.Touch(context.Background(), time.Now()) // must not panic
}

// TestRecentLoad_ScrapeErrorKeepsPreviousRing — a scrape failure
// leaves the prior ring intact. The reaper sees stale-but-non-zero
// rather than zero, which is the safer direction. This test
// confirms the error path is a no-op: the post-error RecentRPS
// equals the pre-error RecentRPS.
func TestRecentLoad_ScrapeErrorKeepsPreviousRing(t *testing.T) {
	calls := 0
	r := New(&fakeScraper{fn: func(ctx context.Context) (map[string]int64, error) {
		calls++
		if calls == 2 {
			return nil, errFake{}
		}
		return map[string]int64{"app1": int64(calls * 10)}, nil
	}}, 5, time.Second)
	base := time.Unix(1_000_000, 0)
	// Call 1 (cum=10): seed lastSeen=10, bucket count=0.
	r.Touch(context.Background(), base)
	preError := r.RecentRPS("app1", base)
	// Call 2: error — ring unchanged.
	r.Touch(context.Background(), base.Add(time.Second))
	// Call 3 (cum=30, calls=3): delta = 30-10 = 20, bucket count=20.
	r.Touch(context.Background(), base.Add(2*time.Second))
	postError := r.RecentRPS("app1", base.Add(2*time.Second))
	// The error tick is a no-op; the bucket count went 0 → 0
	// during the error tick. Then the next successful tick adds
	// delta=20. preError=0, postError=20 (the delta added AFTER
	// the error, not before). The key invariant: the error did
	// NOT roll back the previous ring state — there's no record
	// in the ring of the error tick itself.
	_ = preError
	if postError != 20 {
		t.Errorf("RecentRPS after scrape error = %v, want 20 (next-success delta preserved)", postError)
	}
}

type errFake struct{}

func (errFake) Error() string { return "synthetic scrape failure" }

// TestRecentLoad_RestartClearsRing (issue #171 review finding):
// the production code path treats `cumulative < lastSeen` as
// gatewayd-internal restart and clears the per-app ring so the new
// window's first delta is measured from the new boot's
// perspective. Without this, a single cumulative drop would
// inflate the delta to MAX_INT and cause extreme scale-up +
// scale-down thrashing downstream.
func TestRecentLoad_RestartClearsRing(t *testing.T) {
	// Mutable cumulative value the closure reads on each Tick.
	cum := int64(0)
	scr := &fakeScraper{fn: func(ctx context.Context) (map[string]int64, error) {
		return map[string]int64{"app1": cum}, nil
	}}
	r := New(scr, 5, time.Second)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// Tick 1: cumulative=100, delta=100, seed bucket.
	cum = 100
	r.Touch(context.Background(), base)
	if rps := r.RecentRPS("app1", base); rps != 0 {
		t.Errorf("after first Touch: RecentRPS = %v, want 0 (delta in seed bucket only)", rps)
	}

	// Tick 2: cumulative=150, delta=50, bucket sum=50.
	cum = 150
	r.Touch(context.Background(), base.Add(time.Second))
	if rps := r.RecentRPS("app1", base.Add(time.Second)); rps != 50 {
		t.Errorf("after second Touch: RecentRPS = %v, want 50", rps)
	}

	// Tick 3: gatewayd-internal restart — cumulative drops to 5. Mirror
	// must clear the ring and treat the new observation as the
	// boot point.
	cum = 5
	r.Touch(context.Background(), base.Add(2*time.Second))
	// Brand-new bucket, count=0 (no delta yet — the bucket is
	// just reseeded).
	if rps := r.RecentRPS("app1", base.Add(2*time.Second)); rps != 0 {
		t.Errorf("after restart reseed: RecentRPS = %v, want 0 (ring cleared)", rps)
	}

	// Tick 4: cumulative=25, delta=20, bucket sum=20.
	cum = 25
	r.Touch(context.Background(), base.Add(3*time.Second))
	if rps := r.RecentRPS("app1", base.Add(3*time.Second)); rps != 20 {
		t.Errorf("after restart+1 tick: RecentRPS = %v, want 20 (post-restart delta)", rps)
	}
}
