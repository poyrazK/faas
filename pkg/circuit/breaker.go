// Package circuit is the three-state circuit breaker shared by the gateway's
// instance breaker and schedd's egress breaker (ADR-201 §2, §3).
//
// One implementation serves both because the two differ only in what feeds
// them: the gateway reports per-request transport outcomes, while the egress
// breaker reports ADR-098 probe outcomes every 30 s. The state machine,
// thresholds, backoff and half-open discipline are identical, and keeping them
// identical is the point — an operator who has learned how one behaves has
// learned how both behave.
//
// What this package deliberately does NOT do:
//
//   - It does not persist. Breaker state is per-node observation that decays
//     in seconds; a Postgres round-trip in the picker is exactly the hot-path
//     dependency ADR-190 §3 removed. State is rebuilt from observation after
//     a restart, which is correct: a fresh process has no evidence that
//     anything is unhealthy.
//   - It does not time anything itself. Callers report outcomes; the breaker
//     only counts. This keeps it free of goroutines and makes every
//     transition deterministic under test.
package circuit

import (
	"sync"
	"time"
)

// State is the breaker's position in the closed → open → half_open cycle.
type State string

const (
	// StateClosed allows every request. Failures are counted against the
	// rolling window and may trip the breaker open.
	StateClosed State = "closed"
	// StateOpen rejects every request. The target is not selectable until
	// the open duration elapses.
	StateOpen State = "open"
	// StateHalfOpen admits exactly one probe request. Its outcome decides
	// whether the breaker closes or returns to open with a longer backoff.
	StateHalfOpen State = "half_open"
)

// windowBuckets is the resolution of the sliding failure window. Ten buckets
// means a 10 s window ages out in 1 s steps, which is fine-grained enough that
// a burst of failures does not linger a full window after recovery, and coarse
// enough that observing an outcome stays O(10).
const windowBuckets = 10

// Config parameterises one breaker group. The zero value is not useful; use
// DefaultConfig or LegacyQuarantineConfig.
type Config struct {
	// FailureThreshold is the failure ratio within Window at or above which
	// a closed breaker opens. Checked only once MinRequests is satisfied.
	FailureThreshold float64
	// MinRequests is the minimum number of observations within Window before
	// the ratio is consulted at all. This is the guard that stops a single
	// failure on a low-traffic app from opening the circuit — without it, one
	// transport blip on an app serving one request a minute reads as a 100%
	// failure rate.
	MinRequests int
	// Window is the span of the sliding failure window.
	Window time.Duration
	// OpenDuration is how long the first open state lasts before the breaker
	// offers a half-open probe.
	OpenDuration time.Duration
	// MaxOpenDuration caps the exponential backoff. Each re-open without an
	// intervening successful close doubles the open duration up to this bound,
	// so a persistently dead target is probed rarely rather than every
	// OpenDuration forever.
	MaxOpenDuration time.Duration
}

// DefaultConfig is the ADR-201 §2 default: open at a 50% failure ratio over a
// 10 s window once at least 5 outcomes have been seen, probe after 5 s, back
// off to at most 60 s.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 0.5,
		MinRequests:      5,
		Window:           10 * time.Second,
		OpenDuration:     5 * time.Second,
		MaxOpenDuration:  60 * time.Second,
	}
}

// LegacyQuarantineConfig reproduces the pre-ADR-201 ServiceProxy behaviour: a
// single failure benches the endpoint for a flat 5 s with no backoff growth.
// This is what the group is built with when FAAS_GATEWAY_CIRCUIT_BREAKER is
// off, so the flag-off tree is behaviourally identical to the fixed-TTL
// quarantine map it replaces. TestLegacyConfigMatchesQuarantine pins it.
func LegacyQuarantineConfig() Config {
	return Config{
		FailureThreshold: 0,
		MinRequests:      1,
		Window:           5 * time.Second,
		OpenDuration:     5 * time.Second,
		MaxOpenDuration:  5 * time.Second,
	}
}

// EgressConfig is the ADR-201 §3 default for probe-driven breaking. The window
// is much longer and MinRequests much lower than the gateway's because the
// input is a 30 s probe, not per-request traffic: three samples is a
// 90-second-old picture, which is the freshest honest signal available.
func EgressConfig() Config {
	return Config{
		FailureThreshold: 0.5,
		MinRequests:      3,
		Window:           120 * time.Second,
		OpenDuration:     30 * time.Second,
		MaxOpenDuration:  300 * time.Second,
	}
}

// bucket is one slice of the sliding window.
type bucket struct {
	at        time.Time
	successes int
	failures  int
}

// breaker is the per-key state. Guarded by Group.mu; it has no lock of its own
// so that a caller holding the group lock can read several keys consistently.
type breaker struct {
	state   State
	buckets [windowBuckets]bucket
	// openedAt and openFor describe the current open interval. openFor grows
	// by doubling on each re-open and resets to Config.OpenDuration on a
	// successful close.
	openedAt time.Time
	openFor  time.Duration
	// probeInFlight is the half-open mutual exclusion. Exactly one probe is
	// admitted; every other caller is refused until that probe reports.
	probeInFlight bool
	// transitions counts state changes for the metric surface.
	transitions int
}

// Group is a keyed set of breakers sharing one Config — for the gateway, keyed
// by "appID/instanceID"; for egress, by "appID/upstreamHash/port".
//
// Keys are not garbage-collected on their own because an entry is tiny and a
// key's lifetime is bounded by the thing it names (an instance ID is unique
// per instance, an upstream row is bounded by plan quota). Forget is provided
// for the caller that knows a key is retired — the gateway calls it when an
// instance is destroyed, which is what keeps the map bounded across park/wake
// cycles.
type Group struct {
	mu  sync.Mutex
	cfg Config
	now func() time.Time
	m   map[string]*breaker
	// onTransition is an optional observer for the metric surface. It is
	// invoked while the lock is held, so implementations must not call back
	// into the group.
	onTransition func(key string, from, to State)
}

// NewGroup builds a breaker group. A nil clock uses time.Now.
func NewGroup(cfg Config, now func() time.Time) *Group {
	if now == nil {
		now = time.Now
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Second
	}
	if cfg.OpenDuration <= 0 {
		cfg.OpenDuration = time.Second
	}
	if cfg.MaxOpenDuration < cfg.OpenDuration {
		cfg.MaxOpenDuration = cfg.OpenDuration
	}
	if cfg.MinRequests < 1 {
		cfg.MinRequests = 1
	}
	return &Group{cfg: cfg, now: now, m: make(map[string]*breaker)}
}

// WithTransitionObserver registers a callback fired on every state change.
// Returns g for chaining. Not safe to call once the group is in use.
func (g *Group) WithTransitionObserver(fn func(key string, from, to State)) *Group {
	g.onTransition = fn
	return g
}

// Allow reports whether a request to key may proceed, and settles any pending
// open → half_open transition as a side effect.
//
// In half_open it returns true for exactly one caller. That caller owns the
// probe and MUST report its outcome via Success or Failure, or the breaker
// stays half-open with a permanently reserved probe slot. Every call site in
// this repo reports through a deferred call for that reason.
func (g *Group) Allow(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.at(key)
	now := g.now()

	if b.state == StateOpen {
		if now.Sub(b.openedAt) < b.openFor {
			return false
		}
		g.transition(key, b, StateHalfOpen)
	}
	if b.state == StateHalfOpen {
		if b.probeInFlight {
			return false
		}
		b.probeInFlight = true
		return true
	}
	return true
}

// Success reports a successful outcome for key.
func (g *Group) Success(key string) { g.observe(key, true) }

// Failure reports a failed outcome for key.
func (g *Group) Failure(key string) { g.observe(key, false) }

func (g *Group) observe(key string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.at(key)
	now := g.now()

	if b.state == StateHalfOpen {
		// The probe decides, on its own, with no reference to the window —
		// the window describes the traffic that tripped the breaker, not this
		// single trial.
		b.probeInFlight = false
		if ok {
			g.reset(b)
			g.transition(key, b, StateClosed)
			b.openFor = g.cfg.OpenDuration
			return
		}
		g.open(key, b, now)
		return
	}

	g.record(b, now, ok)
	if b.state != StateClosed {
		return
	}
	successes, failures := g.totals(b, now)
	total := successes + failures
	if total < g.cfg.MinRequests || failures == 0 {
		return
	}
	if float64(failures)/float64(total) >= g.cfg.FailureThreshold {
		g.open(key, b, now)
	}
}

// State reports the current state for key, settling an elapsed open interval
// into half_open so a read is never stale. It does not reserve a probe slot;
// only Allow does that.
func (g *Group) State(key string) State {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.at(key)
	if b.state == StateOpen && g.now().Sub(b.openedAt) >= b.openFor {
		g.transition(key, b, StateHalfOpen)
	}
	return b.state
}

// Forget drops all state for key. Called when the underlying target is gone
// (instance destroyed, upstream row deleted) so the map stays bounded.
func (g *Group) Forget(key string) {
	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
}

// Len reports the number of tracked keys. Used by the residency test that
// asserts park/wake cycles do not grow the map.
func (g *Group) Len() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.m)
}

// at returns the breaker for key, creating a closed one on first use.
func (g *Group) at(key string) *breaker {
	b, ok := g.m[key]
	if !ok {
		b = &breaker{state: StateClosed, openFor: g.cfg.OpenDuration}
		g.m[key] = b
	}
	return b
}

// open moves a breaker into the open state and doubles the next backoff.
func (g *Group) open(key string, b *breaker, now time.Time) {
	// Double from the interval just served, not from the base, so a target
	// that keeps failing its probe is backed off geometrically rather than
	// re-probed every OpenDuration forever.
	next := b.openFor
	if b.state == StateHalfOpen {
		next *= 2
	}
	if next > g.cfg.MaxOpenDuration {
		next = g.cfg.MaxOpenDuration
	}
	if next < g.cfg.OpenDuration {
		next = g.cfg.OpenDuration
	}
	b.openFor = next
	b.openedAt = now
	b.probeInFlight = false
	g.reset(b)
	g.transition(key, b, StateOpen)
}

func (g *Group) transition(key string, b *breaker, to State) {
	if b.state == to {
		return
	}
	from := b.state
	b.state = to
	b.transitions++
	if g.onTransition != nil {
		g.onTransition(key, from, to)
	}
}

// record increments the bucket covering now, aging out any bucket that has
// fallen outside the window.
func (g *Group) record(b *breaker, now time.Time, ok bool) {
	span := g.cfg.Window / windowBuckets
	if span <= 0 {
		span = time.Nanosecond
	}
	idx := int(now.UnixNano()/int64(span)) % windowBuckets
	if idx < 0 {
		idx += windowBuckets
	}
	slot := &b.buckets[idx]
	// A bucket whose timestamp is older than one full window belongs to a
	// previous revolution of the ring; reuse it rather than adding to stale
	// counts.
	if now.Sub(slot.at) >= g.cfg.Window {
		slot.successes, slot.failures = 0, 0
	}
	slot.at = now
	if ok {
		slot.successes++
	} else {
		slot.failures++
	}
}

// totals sums the buckets still inside the window.
func (g *Group) totals(b *breaker, now time.Time) (successes, failures int) {
	for i := range b.buckets {
		slot := &b.buckets[i]
		if slot.at.IsZero() || now.Sub(slot.at) >= g.cfg.Window {
			continue
		}
		successes += slot.successes
		failures += slot.failures
	}
	return successes, failures
}

func (g *Group) reset(b *breaker) {
	for i := range b.buckets {
		b.buckets[i] = bucket{}
	}
}
