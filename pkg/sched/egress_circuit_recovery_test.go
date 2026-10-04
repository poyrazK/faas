// adr: 531
package sched

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/netns"
)

func circuitHistory(up EgressUpstream, now time.Time) []EgressCircuitCandidate {
	return []EgressCircuitCandidate{
		{Upstream: up, Sampled: now.Add(-90 * time.Second)},
		{Upstream: up, Sampled: now.Add(-60 * time.Second)},
		{Upstream: up, Sampled: now.Add(-30 * time.Second)},
	}
}

func TestEgressLoopReplaysHistoryAndRepushesWithoutNewSamples(t *testing.T) {
	up := testUpstream()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	history := circuitHistory(up, now)
	read := func(context.Context) ([]EgressCircuitCandidate, error) { return history, nil }
	l, b, applier, clock := loopFixture(t, read)
	*clock = now
	if err := l.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.State(up) == circuit.StateClosed || len(applier.last()) != 1 {
		t.Fatal("startup did not reconstruct sustained failures")
	}
	before := len(applier.pushes)
	if err := l.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(applier.pushes) != before+1 || len(applier.last()) != 1 {
		t.Fatal("unchanged samples did not repair data plane")
	}
}

func TestEgressLoopRetriesFailureWithoutRefoldingProbe(t *testing.T) {
	up := testUpstream()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	history := circuitHistory(up, now)
	l, _, applier, clock := loopFixture(t, func(context.Context) ([]EgressCircuitCandidate, error) { return history, nil })
	*clock = now
	applier.err = errors.New("node unavailable")
	if err := l.Tick(t.Context()); err == nil {
		t.Fatal("failed fanout acknowledged")
	}
	applier.err = nil
	if err := l.Tick(t.Context()); err != nil || len(applier.last()) != 1 {
		t.Fatalf("same sample could not retry enforcement: %v", err)
	}
}

func TestEgressRestartDNSFailurePreservesDurableWholeAppPolicy(t *testing.T) {
	up := testUpstream()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	history := circuitHistory(up, now)
	l, b, applier, clock := loopFixture(t, func(context.Context) ([]EgressCircuitCandidate, error) { return history, nil })
	*clock = now
	b.resolve = func(context.Context, string) ([]string, error) { return nil, errors.New("DNS unavailable") }
	if err := l.Tick(t.Context()); err == nil {
		t.Fatal("DNS outage was acknowledged")
	}
	if len(applier.pushes) != 0 {
		t.Fatal("restart with no local targets overwrote durable policy")
	}
	b.resolve = staticResolver("203.0.113.9")
	if err := l.Tick(t.Context()); err != nil || len(applier.last()) != 1 {
		t.Fatalf("DNS recovery did not retry unchanged evidence: %v", err)
	}
}

func TestEgressLoopRefreshesAllDNSAnswersAndRetriesOptOut(t *testing.T) {
	up := testUpstream()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	history := circuitHistory(up, now)
	l, b, applier, clock := loopFixture(t, func(context.Context) ([]EgressCircuitCandidate, error) { return history, nil })
	*clock = now
	addresses := []string{"203.0.113.9", "2001:db8::1", "203.0.113.9"}
	b.resolve = func(context.Context, string) ([]string, error) { return addresses, nil }
	if err := l.Tick(t.Context()); err != nil || len(applier.last()) != 2 {
		t.Fatalf("all addresses not installed: %v %v", applier.last(), err)
	}
	addresses = []string{"203.0.113.10"}
	if err := l.Tick(t.Context()); err != nil || len(applier.last()) != 1 || applier.last()[0].Addr.String() != addresses[0] {
		t.Fatal("unchanged probe did not refresh changed DNS")
	}
	history = nil
	applier.err = errors.New("node unavailable")
	if err := l.Tick(t.Context()); err == nil {
		t.Fatal("missed opt-out close acknowledged")
	}
	applier.err = nil
	// Production includes durable app IDs even when the final candidate is gone.
	l.WithAppLister(func(context.Context) ([]string, error) { return []string{up.AppID}, nil })
	if err := l.Tick(t.Context()); err != nil || len(applier.last()) != 0 {
		t.Fatal("missed opt-out removal was not retried")
	}
}

func TestEgressLoopReleasesCircuitWhenProbeEvidenceExpires(t *testing.T) {
	up := testUpstream()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	history := circuitHistory(up, now)
	l, b, applier, clock := loopFixture(t, func(context.Context) ([]EgressCircuitCandidate, error) { return history, nil })
	*clock = now
	if err := l.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	*clock = now.Add(3 * time.Minute)
	if err := l.Tick(t.Context()); err != nil || len(applier.last()) != 0 || b.State(up) != circuit.StateClosed {
		t.Fatal("expired evidence left an open circuit")
	}
}

func TestEgressBreakerUsesCustomerThresholds(t *testing.T) {
	up := testUpstream()
	min, seconds, threshold := 1, 60, 1.0
	up.MinSamples, up.OpenSeconds, up.FailureThreshold = &min, &seconds, &threshold
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil).WithClock(func() time.Time { return now })
	if err := b.Observe(t.Context(), up, false); err != nil || b.State(up) != circuit.StateOpen {
		t.Fatal("configured one-sample threshold ignored")
	}
	now = now.Add(40 * time.Second)
	if b.State(up) != circuit.StateOpen {
		t.Fatal("configured 60-second open interval ignored")
	}
}

type durableCircuitStoreFake struct {
	snapshot netns.EgressCircuitSnapshot
	calls    int
}

func (s *durableCircuitStoreFake) PutAppEgressCircuits(_ context.Context, _ string, targets []netns.EgressCircuitTarget) (netns.EgressCircuitSnapshot, error) {
	s.calls++
	s.snapshot = netns.EgressCircuitSnapshot{Revision: 9, Targets: targets}
	return s.snapshot, nil
}

type durableCircuitRouterFake struct {
	store *durableCircuitStoreFake
	seen  netns.EgressCircuitSnapshot
}

func (*durableCircuitRouterFake) UpdateEgressCircuit(context.Context, string, string, []netns.EgressCircuitTarget) error {
	return errors.New("unversioned push")
}
func (r *durableCircuitRouterFake) UpdateEgressCircuitRevision(_ context.Context, _, _ string, snapshot netns.EgressCircuitSnapshot) error {
	if r.store.calls == 0 {
		return errors.New("push preceded durable commit")
	}
	r.seen = snapshot
	return nil
}
func TestEgressApplierCommitsBeforeVersionedFanout(t *testing.T) {
	store := &durableCircuitStoreFake{}
	router := &durableCircuitRouterFake{store: store}
	a := NewRoutedEgressCircuitApplier(router, func(context.Context, string) ([]string, error) { return []string{"new-node"}, nil }).WithDesiredStore(store)
	if err := a.ApplyEgressCircuits(t.Context(), "app", nil); err != nil || !reflect.DeepEqual(router.seen, store.snapshot) {
		t.Fatalf("durable fanout error=%v snapshot=%v", err, router.seen)
	}
}
