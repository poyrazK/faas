// adr: 195
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

type fakeApplier struct {
	opened []netns.EgressCircuitTarget
	closed []netns.EgressCircuitTarget
	err    error
}

func (f *fakeApplier) OpenEgressCircuit(_ context.Context, _ string, t netns.EgressCircuitTarget) error {
	if f.err != nil {
		return f.err
	}
	f.opened = append(f.opened, t)
	return nil
}

func (f *fakeApplier) CloseEgressCircuit(_ context.Context, _ string, t netns.EgressCircuitTarget) error {
	if f.err != nil {
		return f.err
	}
	f.closed = append(f.closed, t)
	return nil
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
	if len(applier.opened) != 1 {
		t.Fatalf("opened = %v, want exactly one reject rule installed", applier.opened)
	}
	if got := applier.opened[0].Addr.String(); got != "203.0.113.9" {
		t.Fatalf("installed address = %q, want the resolved upstream", got)
	}
	if applier.opened[0].Port != 5432 {
		t.Fatalf("installed port = %d, want 5432", applier.opened[0].Port)
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
	if len(applier.opened) != 0 {
		t.Fatalf("opened = %v, want no enforcement on a single blip", applier.opened)
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
	if len(applier.opened) != 1 {
		t.Fatalf("opened = %v, want one", applier.opened)
	}
	// A success while still inside the open interval is recorded but does
	// not close the circuit, and must not disturb the installed rule.
	if err := b.Observe(ctx, up, true); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(applier.closed) != 0 {
		t.Fatalf("closed = %v, want the rule to survive until the circuit actually closes", applier.closed)
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
	if len(applier.opened) != 1 {
		t.Fatalf("opened = %v, want exactly one install across repeated failures", applier.opened)
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
	if len(applier.opened) != 0 {
		t.Fatalf("opened = %v, want nothing enforced without a resolved address", applier.opened)
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
	if len(applier.opened) != 0 {
		t.Fatalf("opened = %v, want no enforcement in report-only mode", applier.opened)
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
	if len(applier.closed) != 1 {
		t.Fatalf("closed = %v, want the rule removed on Forget", applier.closed)
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
	if len(applier.opened) != 1 {
		t.Fatalf("opened = %v, want only the failing upstream enforced", applier.opened)
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
	if len(applier.opened) != 1 {
		t.Fatalf("opened = %v, want the rule installed", applier.opened)
	}

	// The upstream recovers; the next probe lands in half_open and closes it.
	clock = clock.Add(30 * time.Second)
	if err := b.Observe(ctx, up, true); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got := b.State(up); got != circuit.StateClosed {
		t.Fatalf("state = %q, want closed after a successful half-open probe", got)
	}
	if len(applier.closed) != 1 {
		t.Fatalf("closed = %v, want the rule removed exactly once on close", applier.closed)
	}
	if got := b.OpenCircuits(); len(got) != 0 {
		t.Fatalf("OpenCircuits = %v, want empty once the dependency is healthy again", got)
	}
}
