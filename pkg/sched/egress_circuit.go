package sched

// Egress circuit breaking (ADR-197 §3).
//
// This is the half of the feature that decides; pkg/netns owns the half that
// enforces. The decision input is the ADR-098 probe: meterd already dials
// every captured upstream every 30 s with crypto/tls.Dial and classifies the
// result, so the health signal needs no new data plane, no L7 egress proxy,
// and no TLS interception into tenant traffic.
//
// What an open circuit buys the customer: without it, an app whose database
// is blackholed pays a full TCP connect timeout on every request. That burns
// the kind=budget deadline, then holds the wake slot for the duration, so one
// dependency outage becomes a per-app capacity outage. With the circuit open
// the connect fails immediately with ECONNREFUSED and the app's own error
// handling runs in microseconds.
//
// The half-open trial is deliberately driven by the PROBE, never by tenant
// traffic: the customer's request should not be the canary for their own
// dependency's recovery.

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/netns"
)

// EgressUpstream identifies one breakable dependency of one app.
//
// Hash is the §11-safe identifier (data_upstreams.host_redacted_hash) used in
// every metric label, log line and API response. Host is the plaintext value
// needed to resolve an address for the nftables element; it is carried here
// because the breaker runs inside schedd against the row it already holds,
// and it MUST NOT reach a label, a log, or the wire.
type EgressUpstream struct {
	AppID string
	Hash  string
	Host  string
	Port  int
}

// key is the breaker-group key. Built from the hash, never the host, so a
// panic dump or a breaker-state export cannot leak a customer's database
// hostname.
func (u EgressUpstream) key() string {
	return u.AppID + "\x00" + u.Hash + "\x00" + fmt.Sprint(u.Port)
}

// EgressCircuitApplier installs and removes reject rules in the netns of every
// live instance of an app. The production implementation goes through vmmd,
// which remains the only component that touches a network namespace.
type EgressCircuitApplier interface {
	OpenEgressCircuit(ctx context.Context, appID string, target netns.EgressCircuitTarget) error
	CloseEgressCircuit(ctx context.Context, appID string, target netns.EgressCircuitTarget) error
}

// EgressResolver maps an upstream host to the address the guest would reach.
// Injected so the breaker is testable without DNS, and so a future
// per-node resolver can be swapped in.
type EgressResolver func(ctx context.Context, host string) (string, error)

// EgressCircuitBreaker drives circuit state for declared upstreams from probe
// outcomes and applies the result to the data plane.
type EgressCircuitBreaker struct {
	group    *circuit.Group
	applier  EgressCircuitApplier
	resolve  EgressResolver
	log      *slog.Logger
	onChange func(appID, hash string, from, to circuit.State)

	mu sync.Mutex
	// open tracks the target currently installed per key, so a close
	// deletes exactly the element that was added. nft errors on deleting a
	// element that is not present, and re-resolving at close time could
	// produce a different address after a DNS change.
	open map[string]netns.EgressCircuitTarget
}

// NewEgressCircuitBreaker builds a breaker over circuit.EgressConfig. A nil
// logger uses slog.Default; a nil resolver disables enforcement (state is
// still tracked, which is the report-only posture an operator can run first).
func NewEgressCircuitBreaker(applier EgressCircuitApplier, resolve EgressResolver, log *slog.Logger) *EgressCircuitBreaker {
	if log == nil {
		log = slog.Default()
	}
	b := &EgressCircuitBreaker{
		applier: applier,
		resolve: resolve,
		log:     log,
		open:    make(map[string]netns.EgressCircuitTarget),
	}
	b.group = circuit.NewGroup(circuit.EgressConfig(), nil)
	return b
}

// WithChangeObserver registers a transition callback for the metric surface.
func (b *EgressCircuitBreaker) WithChangeObserver(fn func(appID, hash string, from, to circuit.State)) *EgressCircuitBreaker {
	b.onChange = fn
	return b
}

// WithClock rebuilds the breaker group on a caller-supplied clock. Tests use
// it to advance through the 30 s open interval without sleeping; production
// leaves it unset. Must be called before the first Observe — it discards any
// state the group already holds.
func (b *EgressCircuitBreaker) WithClock(now func() time.Time) *EgressCircuitBreaker {
	b.group = circuit.NewGroup(circuit.EgressConfig(), now)
	return b
}

// Observe reports one probe outcome for an upstream and reconciles the data
// plane if the state changed.
//
// ok maps directly from data_upstream_probes.ok. A tls_handshake failure
// counts as a failure even though TCP succeeded: the dependency is reachable
// but unusable, which is exactly the case an app cannot distinguish and would
// otherwise retry against forever.
func (b *EgressCircuitBreaker) Observe(ctx context.Context, up EgressUpstream, ok bool) error {
	key := up.key()
	before := b.group.State(key)
	if ok {
		b.group.Success(key)
	} else {
		b.group.Failure(key)
	}
	after := b.group.State(key)
	if before != after && b.onChange != nil {
		b.onChange(up.AppID, up.Hash, before, after)
	}
	return b.reconcile(ctx, up, after)
}

// State reports the current circuit state for an upstream. Used by the API
// surface so a customer can see why their dependency calls are failing fast.
func (b *EgressCircuitBreaker) State(up EgressUpstream) circuit.State {
	return b.group.State(up.key())
}

// reconcile brings the data plane in line with the breaker state.
//
// The rule is installed in open AND half_open, and removed only on close.
// That asymmetry is deliberate and is the point of driving this from the
// probe: meterd dials the upstream from the HOST, while the reject rule lives
// in the guest's netns, so the probe is entirely unaffected by it. The trial
// therefore costs the tenant nothing, and there is no reason to expose tenant
// traffic to the hang while the circuit is being re-tested. Removing the rule
// at half_open — the obvious reading of a textbook breaker, where the trial
// IS a real request — would reopen exactly the hang this feature removes, for
// every request that arrives during the trial window.
func (b *EgressCircuitBreaker) reconcile(ctx context.Context, up EgressUpstream, state circuit.State) error {
	if b.applier == nil || b.resolve == nil {
		return nil // report-only
	}
	key := up.key()
	b.mu.Lock()
	installed, isInstalled := b.open[key]
	b.mu.Unlock()

	switch state {
	case circuit.StateOpen, circuit.StateHalfOpen:
		if isInstalled {
			return nil
		}
		return b.openCircuit(ctx, up, key)
	case circuit.StateClosed:
		if !isInstalled {
			return nil
		}
		return b.closeCircuit(ctx, up, key, installed)
	}
	return nil
}

func (b *EgressCircuitBreaker) openCircuit(ctx context.Context, up EgressUpstream, key string) error {
	addr, err := b.resolve(ctx, up.Host)
	if err != nil {
		// Fail OPEN in the availability sense: if the host cannot be
		// resolved we cannot write a correct rule, and writing a wrong one
		// would reject traffic the customer needs. The circuit stays
		// logically open (the state machine already moved) but nothing is
		// enforced, and the next probe retries.
		b.log.Warn("sched: egress circuit: resolve failed; not enforcing",
			"app", up.AppID, "upstream", up.Hash, "err", err)
		return nil
	}
	target, err := netns.ParseEgressCircuitTarget(addr, up.Port)
	if err != nil {
		b.log.Warn("sched: egress circuit: unusable target; not enforcing",
			"app", up.AppID, "upstream", up.Hash, "err", err)
		return nil
	}
	if err := b.applier.OpenEgressCircuit(ctx, up.AppID, target); err != nil {
		return fmt.Errorf("sched: egress circuit open %s: %w", up.Hash, err)
	}
	b.mu.Lock()
	b.open[key] = target
	b.mu.Unlock()
	b.log.Warn("sched: egress circuit opened; connections will fail fast",
		"app", up.AppID, "upstream", up.Hash, "port", up.Port)
	return nil
}

func (b *EgressCircuitBreaker) closeCircuit(ctx context.Context, up EgressUpstream, key string, installed netns.EgressCircuitTarget) error {
	if err := b.applier.CloseEgressCircuit(ctx, up.AppID, installed); err != nil {
		return fmt.Errorf("sched: egress circuit close %s: %w", up.Hash, err)
	}
	b.mu.Lock()
	delete(b.open, key)
	b.mu.Unlock()
	b.log.Info("sched: egress circuit closed",
		"app", up.AppID, "upstream", up.Hash, "port", up.Port)
	return nil
}

// Forget drops all state for an upstream. Called when the data_upstreams row
// is deleted so the breaker map cannot outlive its subject.
func (b *EgressCircuitBreaker) Forget(ctx context.Context, up EgressUpstream) {
	key := up.key()
	b.mu.Lock()
	installed, isOpen := b.open[key]
	delete(b.open, key)
	b.mu.Unlock()
	if isOpen && b.applier != nil {
		if err := b.applier.CloseEgressCircuit(ctx, up.AppID, installed); err != nil {
			b.log.Warn("sched: egress circuit: forget could not remove rule",
				"app", up.AppID, "upstream", up.Hash, "err", err)
		}
	}
	b.group.Forget(key)
}

// OpenCircuits returns the currently enforced circuits, sorted for stable
// output. Used by the operator surface and by the leak assertion that a
// parked app leaves no rules behind.
func (b *EgressCircuitBreaker) OpenCircuits() []netns.EgressCircuitTarget {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]netns.EgressCircuitTarget, 0, len(b.open))
	for _, t := range b.open {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr.String() < out[j].Addr.String()
		}
		return out[i].Port < out[j].Port
	})
	return out
}
