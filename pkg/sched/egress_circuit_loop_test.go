// adr: 201
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/netns"
)

func loopFixture(t *testing.T, read EgressCircuitCandidateReader) (*EgressCircuitLoop, *EgressCircuitBreaker, *fakeApplier, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), quietLog()).
		WithClock(func() time.Time { return clock })
	l := NewEgressCircuitLoop(b, read, 30*time.Second, 0, quietLog()).
		WithClock(func() time.Time { return clock })
	return l, b, applier, &clock
}

// The dedupe rule that makes the poll safe: the breaker counts OBSERVATIONS,
// so re-folding the same probe row on every tick would manufacture evidence
// and trip a circuit on a single real sample.
func TestEgressLoopDoesNotRefoldTheSameProbeRow(t *testing.T) {
	up := testUpstream()
	sampled := time.Date(2026, 9, 21, 11, 59, 45, 0, time.UTC)
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		return []EgressCircuitCandidate{{Upstream: up, OK: false, Sampled: sampled}}, nil
	}
	l, b, applier, _ := loopFixture(t, read)
	ctx := context.Background()

	// EgressConfig needs 3 samples. Ten ticks over ONE row must not reach it.
	for i := 0; i < 10; i++ {
		if err := l.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if got := b.State(up); got != circuit.StateClosed {
		t.Fatalf("state = %q after 10 ticks over one probe row, want closed — a poll must not manufacture observations", got)
	}
	if applier.opened() != 0 {
		t.Fatalf("pushes = %v, want nothing enforced from a single sample", applier.pushes)
	}
}

// Genuinely new rows DO advance the breaker.
func TestEgressLoopFoldsSuccessiveProbeRows(t *testing.T) {
	up := testUpstream()
	sampled := time.Date(2026, 9, 21, 11, 59, 45, 0, time.UTC)
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		return []EgressCircuitCandidate{{Upstream: up, OK: false, Sampled: sampled}}, nil
	}
	l, b, applier, clock := loopFixture(t, read)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := l.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		sampled = sampled.Add(30 * time.Second)
		*clock = clock.Add(30 * time.Second)
	}
	if got := b.State(up); got == circuit.StateClosed {
		t.Fatalf("state = %q after 3 distinct failed probes, want open or half_open", got)
	}
	if applier.opened() != 1 {
		t.Fatalf("pushes = %v, want the circuit enforced once", applier.pushes)
	}
}

// A stalled meterd must not freeze a verdict: past maxAge the probe is
// treated as unmeasured and skipped.
func TestEgressLoopSkipsStaleProbes(t *testing.T) {
	up := testUpstream()
	// Sampled ten minutes before the loop's clock; maxAge defaults to 4×30s.
	stale := time.Date(2026, 9, 21, 11, 50, 0, 0, time.UTC)
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		return []EgressCircuitCandidate{{Upstream: up, OK: false, Sampled: stale}}, nil
	}
	l, b, applier, _ := loopFixture(t, read)

	for i := 0; i < 5; i++ {
		_ = l.Tick(context.Background())
	}
	if got := b.State(up); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed — a stale probe is not evidence", got)
	}
	if applier.opened() != 0 {
		t.Fatalf("pushes = %v, want nothing enforced from stale evidence", applier.pushes)
	}
}

// An upstream with no probe at all must never be broken.
func TestEgressLoopSkipsUnprobedUpstreams(t *testing.T) {
	up := testUpstream()
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		return []EgressCircuitCandidate{{Upstream: up, OK: false}}, nil
	}
	l, b, applier, _ := loopFixture(t, read)

	for i := 0; i < 5; i++ {
		_ = l.Tick(context.Background())
	}
	if got := b.State(up); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed for an upstream nothing has measured", got)
	}
	if applier.opened() != 0 {
		t.Fatalf("pushes = %v, want no enforcement", applier.pushes)
	}
}

func TestEgressLoopSurfacesReadErrors(t *testing.T) {
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		return nil, errors.New("postgres down")
	}
	l, _, _, _ := loopFixture(t, read)
	if err := l.Tick(context.Background()); err == nil {
		t.Fatal("Tick returned nil on a read failure; the caller must be able to log and retry")
	}
}

// One app's vmmd being unreachable must not stop the rest of the fleet from
// converging.
func TestEgressLoopContinuesPastOneFailingApp(t *testing.T) {
	bad := EgressUpstream{AppID: "app-bad", Hash: "aaaa", Host: "db1.example", Port: 5432}
	good := EgressUpstream{AppID: "app-good", Hash: "bbbb", Host: "db2.example", Port: 5432}
	sampled := time.Date(2026, 9, 21, 11, 59, 45, 0, time.UTC)

	var observed []string
	failing := &perAppFailApplier{failFor: "app-bad", seen: &observed}
	clock := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	b := NewEgressCircuitBreaker(failing, staticResolver("203.0.113.9"), quietLog()).
		WithClock(func() time.Time { return clock })
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		return []EgressCircuitCandidate{
			{Upstream: bad, OK: false, Sampled: sampled},
			{Upstream: good, OK: false, Sampled: sampled},
		}, nil
	}
	l := NewEgressCircuitLoop(b, read, 30*time.Second, 0, quietLog()).
		WithClock(func() time.Time { return clock })

	for i := 0; i < 3; i++ {
		if err := l.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		sampled = sampled.Add(30 * time.Second)
		clock = clock.Add(30 * time.Second)
	}
	var sawGood bool
	for _, appID := range observed {
		if appID == "app-good" {
			sawGood = true
		}
	}
	if !sawGood {
		t.Fatalf("applier saw %v; a failing app must not block the rest of the fleet", observed)
	}
}

// perAppFailApplier fails every push for one app and records the rest, so a
// test can prove the loop kept going past the broken one.
type perAppFailApplier struct {
	failFor string
	seen    *[]string
}

func (p *perAppFailApplier) ApplyEgressCircuits(_ context.Context, appID string, _ []netns.EgressCircuitTarget) error {
	if appID == p.failFor {
		return errors.New("vmmd unavailable")
	}
	*p.seen = append(*p.seen, appID)
	return nil
}

// Retired candidates must not accumulate dedupe state.
func TestEgressLoopForgetsRetiredCandidates(t *testing.T) {
	up := testUpstream()
	sampled := time.Date(2026, 9, 21, 11, 59, 45, 0, time.UTC)
	present := true
	read := func(context.Context) ([]EgressCircuitCandidate, error) {
		if !present {
			return nil, nil
		}
		return []EgressCircuitCandidate{{Upstream: up, OK: true, Sampled: sampled}}, nil
	}
	l, _, _, _ := loopFixture(t, read)

	_ = l.Tick(context.Background())
	if len(l.seen) != 1 {
		t.Fatalf("seen = %v, want one tracked key", l.seen)
	}
	present = false
	_ = l.Tick(context.Background())
	if len(l.seen) != 0 {
		t.Fatalf("seen = %v, want empty once the candidate retires", l.seen)
	}
}
