// adr: 201
package circuit_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
)

// fakeClock is a manually advanced clock so every transition in these tests is
// deterministic — the breaker never reads the wall clock on its own.
type fakeClock struct{ t time.Time }

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

const key = "app-1/inst-1"

func TestClosedStaysClosedBelowMinRequests(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)

	// Four straight failures is a 100% failure ratio, but MinRequests is 5.
	// This is the low-traffic guard: without it one blip opens the circuit.
	for i := 0; i < 4; i++ {
		g.Failure(key)
	}
	if got := g.State(key); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed (MinRequests not yet satisfied)", got)
	}
	if !g.Allow(key) {
		t.Fatal("Allow = false, want true while closed")
	}
}

func TestOpensAtThresholdAndRefuses(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)

	for i := 0; i < 5; i++ {
		g.Failure(key)
	}
	if got := g.State(key); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open after 5/5 failures", got)
	}
	if g.Allow(key) {
		t.Fatal("Allow = true, want false while open")
	}
}

func TestMixedTrafficBelowRatioStaysClosed(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)

	// 2 failures / 10 total = 0.2. The ratio is evaluated on every
	// observation, not once at the end, so the sequence matters: at the fifth
	// observation (the first point MinRequests is satisfied) this is 2F/3S =
	// 0.4, still under the threshold, and it only improves from there.
	for i := 0; i < 2; i++ {
		g.Failure(key)
	}
	for i := 0; i < 8; i++ {
		g.Success(key)
	}
	if got := g.State(key); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed at a 0.2 failure ratio", got)
	}
}

// TestEvaluatesContinuouslyNotInBatch pins the consequence of evaluating on
// every observation: a front-loaded failure burst trips the breaker at the
// moment MinRequests is satisfied, even if later successes would have brought
// the whole-window ratio back under the threshold. This is intended — it is
// what makes a breaker react in one window rather than one window late — and
// it is the behaviour most likely to look like a bug in an incident review,
// so it is pinned rather than left implicit.
func TestEvaluatesContinuouslyNotInBatch(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)

	for i := 0; i < 4; i++ {
		g.Failure(key)
	}
	// Fifth observation: 4F/1S = 0.8 ≥ 0.5, so it trips here even though the
	// caller is about to report five more successes.
	g.Success(key)
	if got := g.State(key); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open — the ratio is checked at every observation", got)
	}
}

func TestHalfOpenAdmitsExactlyOneProbe(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)
	for i := 0; i < 5; i++ {
		g.Failure(key)
	}

	clk.add(5 * time.Second)
	if got := g.State(key); got != circuit.StateHalfOpen {
		t.Fatalf("state = %q, want half_open after OpenDuration", got)
	}
	if !g.Allow(key) {
		t.Fatal("first Allow in half_open = false, want true (the probe)")
	}
	if g.Allow(key) {
		t.Fatal("second Allow in half_open = true, want false (probe already in flight)")
	}
}

func TestHalfOpenSuccessClosesAndResetsBackoff(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)
	for i := 0; i < 5; i++ {
		g.Failure(key)
	}

	// Trip once and fail the probe so the backoff has grown to 10s.
	clk.add(5 * time.Second)
	g.Allow(key)
	g.Failure(key)
	// Now serve the grown interval and pass the probe.
	clk.add(10 * time.Second)
	g.Allow(key)
	g.Success(key)

	if got := g.State(key); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed after a successful probe", got)
	}
	// Backoff must be back at the base: re-trip and confirm the next probe is
	// offered after OpenDuration, not after the grown interval.
	for i := 0; i < 5; i++ {
		g.Failure(key)
	}
	clk.add(5 * time.Second)
	if got := g.State(key); got != circuit.StateHalfOpen {
		t.Fatalf("state = %q, want half_open — a successful close must reset the backoff", got)
	}
}

func TestHalfOpenFailureDoublesBackoffAndCaps(t *testing.T) {
	clk := newClock()
	cfg := circuit.DefaultConfig()
	cfg.MaxOpenDuration = 20 * time.Second
	g := circuit.NewGroup(cfg, clk.now)
	for i := 0; i < 5; i++ {
		g.Failure(key)
	}

	// 5s → probe fails → 10s
	clk.add(5 * time.Second)
	g.Allow(key)
	g.Failure(key)
	clk.add(5 * time.Second)
	if got := g.State(key); got != circuit.StateOpen {
		t.Fatalf("state = %q at +5s, want still open (backoff doubled to 10s)", got)
	}
	clk.add(5 * time.Second)
	if got := g.State(key); got != circuit.StateHalfOpen {
		t.Fatalf("state = %q at +10s, want half_open", got)
	}

	// 10s → probe fails → 20s → probe fails → capped at 20s, not 40s.
	g.Allow(key)
	g.Failure(key)
	clk.add(20 * time.Second)
	g.Allow(key)
	g.Failure(key)
	clk.add(20 * time.Second)
	if got := g.State(key); got != circuit.StateHalfOpen {
		t.Fatalf("state = %q at +20s, want half_open — backoff must cap at MaxOpenDuration", got)
	}
}

// TestLegacyConfigMatchesQuarantine pins the flag-off equivalence claim in
// ADR-201 §2: with FAAS_GATEWAY_CIRCUIT_BREAKER off, the breaker must behave
// exactly like the fixed 5s ServiceProxy quarantine map it replaces — one
// failure benches the target for 5s flat, with no backoff growth.
func TestLegacyConfigMatchesQuarantine(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.LegacyQuarantineConfig(), clk.now)

	g.Failure(key)
	if g.Allow(key) {
		t.Fatal("Allow = true after one failure, want false (legacy benches on a single failure)")
	}

	clk.add(5 * time.Second)
	if !g.Allow(key) {
		t.Fatal("Allow = false at +5s, want true (legacy TTL is a flat 5s)")
	}
	// Fail the probe; legacy must NOT grow the interval.
	g.Failure(key)
	clk.add(5 * time.Second)
	if !g.Allow(key) {
		t.Fatal("Allow = false at the second +5s, want true — legacy must not back off exponentially")
	}
}

func TestWindowAgesOutFailures(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)

	// Four failures, then wait out the whole window. They must not combine
	// with a later failure to reach MinRequests.
	for i := 0; i < 4; i++ {
		g.Failure(key)
		clk.add(100 * time.Millisecond)
	}
	clk.add(11 * time.Second)
	g.Failure(key)
	if got := g.State(key); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed — failures older than the window must age out", got)
	}
}

func TestEgressConfigTripsOnThreeProbeSamples(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.EgressConfig(), clk.now)

	// The probe samples every 30s; three failures is a 90s-old picture. The
	// clock advances between samples but not after the last one, so the
	// assertion observes the open state rather than a state that has already
	// aged into its half-open probe.
	for i := 0; i < 3; i++ {
		if i > 0 {
			clk.add(30 * time.Second)
		}
		g.Failure(key)
	}
	if got := g.State(key); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open after 3 failed probe samples", got)
	}
	// EgressConfig probes far less often than the gateway breaker: the next
	// trial is offered after 30s, matching one probe interval, so the half-open
	// trial is always driven by a fresh probe and never by tenant traffic.
	clk.add(30 * time.Second)
	if got := g.State(key); got != circuit.StateHalfOpen {
		t.Fatalf("state = %q, want half_open one probe interval later", got)
	}
}

func TestForgetBoundsTheMap(t *testing.T) {
	clk := newClock()
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now)

	for _, k := range []string{"a", "b", "c"} {
		g.Failure(k)
	}
	if got := g.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	g.Forget("b")
	if got := g.Len(); got != 2 {
		t.Fatalf("Len after Forget = %d, want 2 — retired keys must not accumulate", got)
	}
}

func TestTransitionObserverSeesEveryChange(t *testing.T) {
	clk := newClock()
	var got []string
	g := circuit.NewGroup(circuit.DefaultConfig(), clk.now).
		WithTransitionObserver(func(_ string, from, to circuit.State) {
			got = append(got, string(from)+"->"+string(to))
		})

	for i := 0; i < 5; i++ {
		g.Failure(key)
	}
	clk.add(5 * time.Second)
	g.Allow(key)
	g.Success(key)

	want := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(got) != len(want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", got, want)
		}
	}
}
