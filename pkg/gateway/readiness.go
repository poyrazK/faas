// Package gateway — readiness.go owns the readiness probes the Tier
// A7 edge daemons wire to /readyz.
//
// Background: the pre-split gatewayd-internal wired /readyz with `nil`
// (cmd/gatewayd-internal/main.go:878 — `gateway.ControlMux(handler.Metrics(),
// nil)`), which made /readyz return 200 unconditionally
// (pkg/gateway/control.go:37 — `if ready == nil || ready()`). That
// was acceptable for single-box (one daemon, no LB to drain) but
// fails closed wrong after the split: a partial-boot daemon would
// happily accept traffic even though the routing cache, the
// cert-bundle, and the warm-hint subscription are not yet ready.
//
// Tier A7 (ADR-070) introduces two daemons — gatewayd-public and
// gatewayd-internal — each with a real readiness signal:
//
//   - gatewayd-public readiness: routing-cache mirror is hydrated
//     (app_changed/domain_changed pg_notify has fired at least
//     once), AND the cert-sync subscriber is fresh (last receipt
//     within 2× api.CertSyncIntervalSeconds), AND the warm-hint
//     subscriber has converged (last receipt within 2× the
//     broadcast cadence), AND the PG connection is up
//     (pgxpool.Ping succeeded at most N seconds ago).
//
//   - gatewayd-internal readiness: routing-target cache is
//     non-empty (the per-app targetSet has at least one entry OR
//     we just admitted a cold wake that produced one), AND the
//     per-node schedd dial cache has at least one ready client,
//     AND the warm-hint subscriber is fresh, AND the PG
//     connection is up.
//
// Each probe is independently composable. ReadyzProbe.All folds the
// per-component signals into a single ReadyFunc suitable for
// passing to gateway.ControlMux. The zero value is the no-op
// "always ready" probe — wired at boot BEFORE the rest of the
// daemon's components register, so /readyz stays 200 during the
// first 100 ms of boot (the LB scrape interval is 1 s+; an
// instantaneous flip during boot is invisible to operators).
package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// readyState is the atomic unit of ReadySignal — bundling the
// ready bit and the reason string into a single immutable struct
// so Set and Report see a consistent snapshot. Reading the
// signal returns a single Load; writing is a single Store of a
// fresh struct. PR #1091 review Finding 8 lifts the atomic.Pointer
// refactor (originally Finding 6 in pkg/wire) into pkg/gateway
// so the two readiness.go files stay in lockstep. The previous
// shape stored ready in atomic.Bool and reason under a
// sync.RWMutex, so a Report() that landed between
// s.ready.Load() (true) and s.lastReason() ("stale") could
// return (true, "stale") — a stale reason paired with a fresh
// ready bit. The inverse — (false, "") — was also possible.
type readyState struct {
	ready  bool
	reason string
}

// ReadySignal is one component's "I'm ready" bit. Operators add
// signals with ReadyzProbe.Register; the /readyz handler flips to
// 503 when ANY registered signal reports false. Signals are
// independent — there is no priority order, no AND-of-children
// logic. If two signals report the same fact, the second one is
// authoritative (it overwrites the first).
//
// Every method is safe for concurrent use. Set and Report
// publish and observe a single atomic.Pointer to an immutable
// readyState — so the (ready, reason) pair is always observed as
// a consistent snapshot. The probe also notifies its optional metric
// observer after each signal transition.
type ReadySignal struct {
	state    atomic.Pointer[readyState]
	onChange atomic.Pointer[func()]
}

// newReadySignal constructs a ReadySignal pre-set to (ready,
// reason). Used by Register, NewStalenessSignal, NewPGPingSignal
// and any caller that wants an initial state without an extra
// atomic hop. Allocates the initial readyState up front so the
// zero-value ReadySignal can be safely reported before its first
// Set call (Report() falls through to the zero-value state).
func newReadySignal(ready bool, reason string) *ReadySignal {
	s := &ReadySignal{}
	s.state.Store(&readyState{ready: ready, reason: reason})
	return s
}

// NewReadySignalForTest exports newReadySignal under an
// underscore-friendly name so the test binary can construct a
// ReadySignal with a known initial state without exposing the
// lower-case constructor in the public API.
func NewReadySignalForTest(ready bool, reason string) *ReadySignal {
	return newReadySignal(ready, reason)
}

// Set flips the signal's ready bit. reason is optional — pass ""
// for "no human-readable reason needed". The /readyz handler
// surfaces the most recent reason across all registered signals
// when /readyz returns 503. The (ready, reason) pair is published
// as a single atomic.Pointer.Store so a concurrent Report() will
// see either the old state or the new state — never a torn pair.
func (s *ReadySignal) Set(ready bool, reason string) {
	s.state.Store(&readyState{ready: ready, reason: reason})
	if onChange := s.onChange.Load(); onChange != nil {
		(*onChange)()
	}
}

func (s *ReadySignal) setOnChange(onChange func()) {
	if onChange == nil {
		s.onChange.Store(nil)
		return
	}
	fn := onChange
	s.onChange.Store(&fn)
}

// Report returns the current (ready, reason) snapshot. The pair
// is read from a single atomic.Pointer.Load so a concurrent Set()
// either fires fully before Report() reads or fully after — never
// in the middle. A zero-value ReadySignal (never Set) reports
// (false, ""), which is the conservative "not yet ready" answer.
func (s *ReadySignal) Report() (ready bool, reason string) {
	st := s.state.Load()
	if st == nil {
		// Zero-value ReadySignal — never Set. Treat as the
		// pre-Set "not yet ready" state.
		return false, ""
	}
	return st.ready, st.reason
}

// ReadyzProbe is the fan-in point. Daemons Register one ReadySignal
// per component at construction; the /readyz handler calls All().
//
// All returns true iff every registered signal is ready. If no
// signals are registered (the zero state), All returns true — the
// pre-split behaviour, preserved so an early-boot scrape does not
// see a spurious 503.
type ReadyzProbe struct {
	mu            sync.RWMutex
	signals       []*ReadySignal
	readyObserver func(bool, string)
}

// Register adds a new ReadySignal to the probe and returns it so
// the caller can Set bits on it later. New signals default to
// "not ready" with the reason "not yet ready" so /readyz surfaces a
// human-readable string during the first half-second of boot. The
// daemon flips each signal to ready (typically with Set(true, ""))
// as components come up. This is the deliberate behaviour — every
// component must opt IN to ready, never opt OUT.
//
// If a daemon wants the pre-split "always ready" behaviour during
// boot, the caller calls Set(true, "") immediately after Register.
func (p *ReadyzProbe) Register() *ReadySignal {
	s := newReadySignal(false, "not yet ready")
	p.mu.Lock()
	p.signals = append(p.signals, s)
	s.setOnChange(p.notifyObserver)
	p.mu.Unlock()
	p.notifyObserver()
	return s
}

// RegisterSignal adds a pre-constructed *ReadySignal to the probe.
// This is the helper-constructor counterpart to Register: helper
// constructors (NewPGPingSignal, NewStalenessSignal) build a signal
// internally and return it for the caller to drive via Stopper /
// Touch. Register() would build a second placeholder signal;
// RegisterSignal folds the existing one into the probe without
// allocating a duplicate.
//
// Used by PR-B1 (gatewayd-internal /readyz tighten) where the
// production wiring mixes Register (manual schedd-router / nodeCache
// signals) with RegisterSignal (PG ping + warm-hint staleness, both
// helper-constructed). The order in All() is the order of the
// Register / RegisterSignal calls, which is preserved for the
// /readyz reason concat.
func (p *ReadyzProbe) RegisterSignal(s *ReadySignal) {
	if s == nil {
		return
	}
	p.mu.Lock()
	p.signals = append(p.signals, s)
	s.setOnChange(p.notifyObserver)
	p.mu.Unlock()
	p.notifyObserver()
}

// SetReadyObserver mirrors the aggregate /readyz result onto an operator
// metric. It invokes observer once immediately and after every signal change;
// the callback runs without the probe lock held.
func (p *ReadyzProbe) SetReadyObserver(observer func(bool, string)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.readyObserver = observer
	for _, s := range p.signals {
		s.setOnChange(p.notifyObserver)
	}
	p.mu.Unlock()
	p.notifyObserver()
}

func (p *ReadyzProbe) notifyObserver() {
	if p == nil {
		return
	}
	p.mu.RLock()
	observer := p.readyObserver
	p.mu.RUnlock()
	if observer == nil {
		return
	}
	ready, reason := p.All()
	observer(ready, reason)
}

// All returns true iff every registered signal is ready. The
// ready reason returned is the OR of all "not ready" reasons —
// the operator can see at a glance which component is not yet
// up. Reasons are concatenated with "; " for parseability.
func (p *ReadyzProbe) All() (ready bool, reason string) {
	p.mu.RLock()
	signals := make([]*ReadySignal, len(p.signals))
	copy(signals, p.signals)
	p.mu.RUnlock()
	if len(signals) == 0 {
		return true, ""
	}
	ready = true
	var reasons []string
	for _, s := range signals {
		r, why := s.Report()
		if !r {
			ready = false
			if why != "" {
				reasons = append(reasons, why)
			}
		}
	}
	if len(reasons) == 0 {
		return ready, ""
	}
	// Concatenate reasons. Cap at 5 to keep the body small.
	if len(reasons) > 5 {
		reasons = reasons[:5]
	}
	return ready, joinReasons(reasons)
}

// ReadyFunc returns a ReadyFunc suitable for gateway.ControlMux.
// The returned func is safe for concurrent use; the underlying
// All() is RLock-guarded.
func (p *ReadyzProbe) ReadyFunc() ReadyFunc {
	return func() bool {
		ok, _ := p.All()
		return ok
	}
}

func joinReasons(reasons []string) string {
	out := ""
	for i, r := range reasons {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}

// NewStalenessSignal returns a ReadySignal whose bit flips false
// after `stale` elapses since the last Touch. The caller calls
// Touch() on every successful receive (e.g. every pg_notify
// delivery); the helper goroutine flips the bit on staleness.
//
// The helper goroutine lives for the lifetime of the signal;
// callers should typically construct the signal at boot and let
// it run forever. A shutdown hook that flips the signal false on
// SIGTERM prevents a late-arriving Touch from re-enabling a
// draining daemon — see cmd/gatewayd-public/drain.go and
// cmd/gatewayd-internal/drain.go for the canonical wiring.
//
// stale must be positive; pass api.CertSyncIntervalSeconds or
// the warm-hint publish cadence at the call site.
//
// The helper runs at the staleness check cadence (1s by default)
// regardless of Touch rate, so a /readyz scrape sees the flip
// within ~1s of staleness.
//
// Concurrency note: touch() BOTH writes the timestamp AND flips
// the signal ready. The helper goroutine only writes false (on
// staleness). Without the optimistic ready-set on touch, a
// /readyz scrape that lands AFTER the previous tick flipped
// stale but BEFORE the next tick would observe the stale bit —
// even though the touch was fresh. The goroutine catches
// staleness; the touch path is the hot recovery path readers
// (LB probes, ops dashboards) observe.
func NewStalenessSignal(stale time.Duration) (signal *ReadySignal, touch func(), stopper func()) {
	s := newReadySignal(false, "no touch yet")
	var lastTouch atomic.Int64 // unix nanos; 0 = "never touched"
	stop := make(chan struct{})
	done := make(chan struct{})
	// Cadence: half the staleness window so a /readyz scrape sees
	// the staleness flip within ≤stale/2 of the actual timeout. Cap
	// the cadence at 1 s — going faster on long windows (the common
	// 30 s CertSyncInterval case) would burn CPU for no signal gain.
	cadence := stale / 2
	if cadence > time.Second {
		cadence = time.Second
	}
	if cadence < 10*time.Millisecond {
		cadence = 10 * time.Millisecond
	}
	// touch: write the timestamp AND flip the signal ready. The
	// goroutine catches the stale-flip; the touch path is the hot
	// recovery path /readyz scrapes observe.
	touchFn := func() {
		lastTouch.Store(time.Now().UnixNano())
		s.Set(true, "")
	}
	go func() {
		defer close(done)
		t := time.NewTicker(cadence)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				touched := lastTouch.Load()
				if touched == 0 {
					// No touch yet — keep signalling not ready.
					s.Set(false, "no touch yet")
					continue
				}
				age := time.Since(time.Unix(0, touched))
				if age > stale {
					s.Set(false, "stale")
					continue
				}
				// Fresh — re-flip ready so the signal is invariant
				// under tick/touch interleaving. touch() also writes
				// ready, but a tick that arrives just after a stale
				// flip and before the next touch would observe stale
				// state from the prior tick without this re-set.
				s.Set(true, "")
			}
		}
	}()
	stopperFn := func() {
		close(stop)
		<-done
		s.Set(false, "shutting down")
	}
	// No pre-arm. PR #1091 review Finding 8 lifts Finding 7
	// (originally pkg/wire only) into pkg/gateway so the two
	// readiness.go files stay in lockstep. The signal now
	// starts at (false, "no touch yet") and the first tick is
	// the canonical readiness flip. See pkg/wire/readiness.go
	// for the full rationale.
	return s, touchFn, stopperFn
}

// NewPGPingSignal returns a ReadySignal whose bit tracks the liveness
// of a pgxpool.Pool. The helper goroutine pings the pool every
// `every` (default 5 s if zero); on success the bit flips true, on
// failure the bit flips false with the most recent error message as
// the reason.
//
// Pings are bounded: the helper cancels any in-flight ping when the
// next tick fires, so a wedged connection cannot stall the readiness
// loop. The returned stopper should be called on daemon shutdown so
// the bit flips false before the process exits — same pattern as
// NewStalenessSignal's stopper.
//
// every must be positive; pass api.ReplicaHeartbeatIntervalSeconds
// (5 s) at the call site for the post-split daemon shape. The P50
// pgxpool.Ping on EX44 is sub-millisecond when the pool has warm
// connections (per ADR-040 bench); under pool exhaustion the ping
// will block on Connect for up to pgx's DialTimeout (default 5 s).
func NewPGPingSignal(ctx context.Context, pool pinger, every time.Duration) (*ReadySignal, func()) {
	if every <= 0 {
		every = 5 * time.Second
	}
	s := newReadySignal(false, "pg ping not yet attempted")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// First tick is half-every so the daemon has a "we tried" bit
		// in /readyz within ~half a probe interval of boot.
		t := time.NewTicker(every / 2)
		defer t.Stop()
		ping := func() {
			// Bound the ping to half-every; if the pool is wedged
			// (pool.Ping blocks), the next tick cancels this one.
			pctx, cancel := context.WithTimeout(ctx, every/2)
			defer cancel()
			if err := pool.Ping(pctx); err != nil {
				s.Set(false, "pg ping failed: "+err.Error())
				return
			}
			s.Set(true, "")
		}
		// Kick one ping immediately so the bit flips to "ready" the
		// instant the daemon can reach Postgres, instead of after
		// `every/2` of idle time.
		ping()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				s.Set(false, "pg ctx cancelled")
				return
			case <-t.C:
				ping()
			}
		}
	}()
	var stopOnce sync.Once
	stopper := func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
			s.Set(false, "pg ping stopped")
		})
	}
	return s, stopper
}

// pinger is the subset of *pgxpool.Pool we need for NewPGPingSignal.
// Defining it locally avoids dragging pgxpool into every test
// import; the production wiring passes *pgxpool.Pool directly.
type pinger interface {
	Ping(context.Context) error
}
