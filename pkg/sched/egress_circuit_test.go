// adr: 201
package sched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/netns"
)

// fakeApplier records every whole-set push. `pushes` is the sequence of
// desired sets, which is what the production RPC actually receives.
type fakeApplier struct {
	pushes [][]netns.EgressCircuitTarget
	err    error
}

func (f *fakeApplier) ApplyEgressCircuits(_ context.Context, _ string, targets []netns.EgressCircuitTarget) error {
	if f.err != nil {
		return f.err
	}
	cp := make([]netns.EgressCircuitTarget, len(targets))
	copy(cp, targets)
	f.pushes = append(f.pushes, cp)
	return nil
}

// last returns the most recent pushed set, or nil when nothing was pushed.
func (f *fakeApplier) last() []netns.EgressCircuitTarget {
	if len(f.pushes) == 0 {
		return nil
	}
	return f.pushes[len(f.pushes)-1]
}

// opened reports the pushes that installed at least one circuit, which is the
// shape the older per-target assertions used.
func (f *fakeApplier) opened() int {
	n := 0
	for _, p := range f.pushes {
		if len(p) > 0 {
			n++
		}
	}
	return n
}

func staticResolver(addr string) EgressResolver {
	return func(context.Context, string) (string, error) { return addr, nil }
}

func testUpstream() EgressUpstream {
	return EgressUpstream{
		AppID: "app-1",
		Hash:  "a1b2c3d4",
		Host:  "db.customer.example",
		Port:  5432,
	}
}

func TestEgressBreakerOpensAfterSustainedProbeFailures(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	up := testUpstream()
	ctx := context.Background()

	// EgressConfig needs 3 samples at a 50% failure ratio.
	for i := 0; i < 3; i++ {
		if err := b.Observe(ctx, up, false); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if got := b.State(up); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open after 3 failed probes", got)
	}
	if applier.opened() != 1 {
		t.Fatalf("pushes = %v, want exactly one push installing a circuit", applier.pushes)
	}
	last := applier.last()
	if len(last) != 1 || last[0].Addr.String() != "203.0.113.9" || last[0].Port != 5432 {
		t.Fatalf("pushed set = %v, want exactly the resolved upstream on :5432", last)
	}
}

// A single blip must not open a circuit — the probe fires every 30s, so
// over-reacting to one sample would black-hole a healthy dependency for
// 30 seconds on the strength of one packet.
func TestEgressBreakerIgnoresSingleFailure(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	up := testUpstream()

	if err := b.Observe(context.Background(), up, false); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got := b.State(up); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed after one failure", got)
	}
	if applier.opened() != 0 {
		t.Fatalf("pushes = %v, want no enforcement on a single blip", applier.pushes)
	}
}

// The rule must SURVIVE the half-open trial and be removed only on close.
//
// meterd probes the upstream from the host while the reject rule lives in the
// guest's netns, so the trial is unaffected by the rule. Removing it during
// half_open — the textbook behaviour, where the trial is a real request —
// would reopen the hang this feature exists to remove for every tenant
// request arriving in the trial window, and buy nothing in exchange.
func TestEgressBreakerKeepsRuleThroughHalfOpenTrial(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	up := testUpstream()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, up, false)
	}
	if applier.opened() != 1 {
		t.Fatalf("pushes = %v, want one", applier.pushes)
	}
	pushesBefore := len(applier.pushes)
	// A success while still inside the open interval is recorded but does
	// not close the circuit, and must not disturb the installed rule.
	if err := b.Observe(ctx, up, true); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(applier.pushes) != pushesBefore {
		t.Fatalf("pushes = %v, want the set untouched until the circuit actually closes", applier.pushes)
	}
	if got := b.OpenCircuits(); len(got) != 1 {
		t.Fatalf("OpenCircuits = %v, want the circuit still enforced", got)
	}
}

// Reconcile must be idempotent: repeated failures while already open must not
// install the rule again.
func TestEgressBreakerDoesNotReinstallWhileOpen(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	up := testUpstream()
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		_ = b.Observe(ctx, up, false)
	}
	if applier.opened() != 1 {
		t.Fatalf("pushes = %v, want exactly one install across repeated failures", applier.pushes)
	}
}

// An unresolvable host must not enforce anything. Writing a rule against a
// guessed address would reject traffic the customer needs, which is strictly
// worse than the hang the feature exists to prevent.
func TestEgressBreakerDoesNotEnforceWhenResolveFails(t *testing.T) {
	applier := &fakeApplier{}
	resolver := func(context.Context, string) (string, error) {
		return "", errors.New("nxdomain")
	}
	b := NewEgressCircuitBreaker(applier, resolver, nil)
	up := testUpstream()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := b.Observe(ctx, up, false); err != nil {
			t.Fatalf("Observe returned an error for an unresolvable host: %v", err)
		}
	}
	if got := b.State(up); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open — state tracking is independent of enforcement", got)
	}
	if applier.opened() != 0 {
		t.Fatalf("pushes = %v, want nothing enforced without a resolved address", applier.pushes)
	}
}

// Report-only mode: an operator can run the breaker with state tracking and
// no enforcement before flipping the data-plane half on.
func TestEgressBreakerReportOnlyWithoutResolver(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, nil, nil)
	up := testUpstream()

	for i := 0; i < 3; i++ {
		_ = b.Observe(context.Background(), up, false)
	}
	if got := b.State(up); got != circuit.StateOpen {
		t.Fatalf("state = %q, want open in report-only mode", got)
	}
	if applier.opened() != 0 {
		t.Fatalf("pushes = %v, want no enforcement in report-only mode", applier.pushes)
	}
}

// Forget must remove any installed rule. A data_upstreams row can be deleted
// while its circuit is open, and a rule that outlives its subject would
// silently black-hole a destination nothing is tracking any more.
func TestEgressBreakerForgetRemovesInstalledRule(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	up := testUpstream()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, up, false)
	}
	b.Forget(ctx, up)
	if got := applier.last(); len(got) != 0 {
		t.Fatalf("final pushed set = %v, want empty after Forget", got)
	}
	if got := b.OpenCircuits(); len(got) != 0 {
		t.Fatalf("OpenCircuits = %v, want empty", got)
	}
}

// §11: the breaker key and every observer callback must carry the redacted
// hash, never the plaintext host.
func TestEgressBreakerNeverKeysOnPlaintextHost(t *testing.T) {
	up := testUpstream()
	if strings.Contains(up.key(), up.Host) {
		t.Fatalf("breaker key %q contains the plaintext host; §11 requires the redacted hash", up.key())
	}
	if !strings.Contains(up.key(), up.Hash) {
		t.Fatalf("breaker key %q does not carry the redacted hash", up.key())
	}

	var seenHash string
	b := NewEgressCircuitBreaker(&fakeApplier{}, staticResolver("203.0.113.9"), nil).
		WithChangeObserver(func(_, hash string, _, _ circuit.State) { seenHash = hash })
	for i := 0; i < 3; i++ {
		_ = b.Observe(context.Background(), up, false)
	}
	if seenHash != up.Hash {
		t.Fatalf("observer saw %q, want the redacted hash %q", seenHash, up.Hash)
	}
}

// Two upstreams of the same app on different ports are independent circuits —
// a broken Postgres must not fail-fast the app's Redis.
func TestEgressBreakerIsolatesUpstreamsByPort(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	pg := testUpstream()
	redis := EgressUpstream{AppID: "app-1", Hash: "deadbeef", Host: "cache.example", Port: 6379}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, pg, false)
		_ = b.Observe(ctx, redis, true)
	}
	if got := b.State(pg); got != circuit.StateOpen {
		t.Fatalf("postgres state = %q, want open", got)
	}
	if got := b.State(redis); got != circuit.StateClosed {
		t.Fatalf("redis state = %q, want closed — one failing dependency must not break another", got)
	}
	if got := applier.last(); len(got) != 1 || got[0].Port != 5432 {
		t.Fatalf("pushed set = %v, want only the failing upstream enforced", got)
	}
}

// The full recovery arc: open, survive the trial window, then close and
// remove the rule once a probe succeeds in half_open. A breaker that never
// closes is just a slow outage.
func TestEgressBreakerClosesAndRemovesRuleAfterRecovery(t *testing.T) {
	clock := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil).
		WithClock(func() time.Time { return clock })
	up := testUpstream()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, up, false)
		clock = clock.Add(30 * time.Second)
	}
	if got := b.State(up); got == circuit.StateClosed {
		t.Fatalf("state = %q, want open or half_open after 3 failed probes", got)
	}
	if applier.opened() != 1 {
		t.Fatalf("pushes = %v, want the rule installed", applier.pushes)
	}

	// The upstream recovers; the next probe lands in half_open and closes it.
	clock = clock.Add(30 * time.Second)
	if err := b.Observe(ctx, up, true); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got := b.State(up); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed after a successful half-open probe", got)
	}
	if got := applier.last(); len(got) != 0 {
		t.Fatalf("final pushed set = %v, want empty once the dependency is healthy again", got)
	}
	if got := b.OpenCircuits(); len(got) != 0 {
		t.Fatalf("OpenCircuits = %v, want empty once the dependency is healthy again", got)
	}
}

// Whole-set semantics: with two upstreams of one app open, every push must
// carry BOTH. Pushing only the newly-transitioned one would flush the other
// out of the set and silently un-break a dependency that is still down.
func TestEgressBreakerPushesUnionOfAppCircuits(t *testing.T) {
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	pg := EgressUpstream{AppID: "app-1", Hash: "aaaa", Host: "db.example", Port: 5432}
	redis := EgressUpstream{AppID: "app-1", Hash: "bbbb", Host: "cache.example", Port: 6379}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, pg, false)
	}
	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, redis, false)
	}

	last := applier.last()
	if len(last) != 2 {
		t.Fatalf("final pushed set = %v, want both open circuits — a push must carry the app's union", last)
	}
	ports := map[int]bool{}
	for _, tgt := range last {
		ports[tgt.Port] = true
	}
	if !ports[5432] || !ports[6379] {
		t.Fatalf("pushed ports = %v, want both 5432 and 6379", ports)
	}
}

// Closing one of two open circuits must leave the other installed.
func TestEgressBreakerCloseKeepsSiblingCircuit(t *testing.T) {
	clock := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	applier := &fakeApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil).
		WithClock(func() time.Time { return clock })
	pg := EgressUpstream{AppID: "app-1", Hash: "aaaa", Host: "db.example", Port: 5432}
	redis := EgressUpstream{AppID: "app-1", Hash: "bbbb", Host: "cache.example", Port: 6379}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, pg, false)
		_ = b.Observe(ctx, redis, false)
		clock = clock.Add(30 * time.Second)
	}
	if got := applier.last(); len(got) != 2 {
		t.Fatalf("pushed set = %v, want both circuits open", got)
	}

	// Postgres recovers; Redis is still down.
	clock = clock.Add(30 * time.Second)
	_ = b.Observe(ctx, pg, true)

	last := applier.last()
	if len(last) != 1 || last[0].Port != 6379 {
		t.Fatalf("pushed set = %v, want only the still-failing redis circuit", last)
	}
}

// Different apps must never share a pushed set — one tenant's broken
// dependency cannot appear in another tenant's firewall.
func TestEgressBreakerNeverMixesAppsInOnePush(t *testing.T) {
	var seen []string
	applier := &recordingAppApplier{}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	a1 := EgressUpstream{AppID: "app-1", Hash: "aaaa", Host: "db1.example", Port: 5432}
	a2 := EgressUpstream{AppID: "app-2", Hash: "bbbb", Host: "db2.example", Port: 5433}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = b.Observe(ctx, a1, false)
		_ = b.Observe(ctx, a2, false)
	}
	for _, p := range applier.pushes {
		seen = append(seen, p.appID)
		if len(p.targets) != 1 {
			t.Fatalf("push for %s carried %d targets, want 1 — apps must not share a set", p.appID, len(p.targets))
		}
	}
	if len(seen) == 0 {
		t.Fatal("no pushes recorded")
	}
}

type appPush struct {
	appID   string
	targets []netns.EgressCircuitTarget
}

type recordingAppApplier struct{ pushes []appPush }

func (r *recordingAppApplier) ApplyEgressCircuits(_ context.Context, appID string, targets []netns.EgressCircuitTarget) error {
	cp := make([]netns.EgressCircuitTarget, len(targets))
	copy(cp, targets)
	r.pushes = append(r.pushes, appPush{appID: appID, targets: cp})
	return nil
}

// A failed push must roll the breaker's view back, or it believes a
// dependency is being blocked when the data plane never got the rule.
func TestEgressBreakerRollsBackOnPushFailure(t *testing.T) {
	applier := &fakeApplier{err: errors.New("vmmd unavailable")}
	b := NewEgressCircuitBreaker(applier, staticResolver("203.0.113.9"), nil)
	up := testUpstream()
	ctx := context.Background()

	var lastErr error
	for i := 0; i < 3; i++ {
		lastErr = b.Observe(ctx, up, false)
	}
	if lastErr == nil {
		t.Fatal("Observe returned nil after a failed push; the error must surface so the next probe retries")
	}
	if got := b.OpenCircuits(); len(got) != 0 {
		t.Fatalf("OpenCircuits = %v, want empty — a failed push must not leave the breaker believing it enforced", got)
	}
}
