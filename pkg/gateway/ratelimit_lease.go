// Package gateway — rate-limit token leasing (ADR-1520).
//
// In central mode every request used to consume its token from Postgres,
// about two control-plane writes per app request (an app and an account
// counter). With leasing, a gateway debits tokens from the shared counter in
// leases and spends them locally. A request that finds no lease is admitted
// from a small local allowance and charged with the next lease, so a request
// within its limit never waits for Postgres. Only traffic at or near its
// limit waits, and an empty bucket answers 429 without a statement until it
// should have refilled.
package gateway

import (
	"context"
	"math"
	"sync"
	"time"
)

// LeaseConfig tunes central-mode token leasing. Zero fields take the ADR's
// defaults. They are gateway tunables, not plan limits.
type LeaseConfig struct {
	// Window is how long a lease should last at the counter's recent rate.
	Window time.Duration
	// Fraction caps one lease at this share of the bucket's burst.
	Fraction float64
	// AllowanceFraction sizes the local allowance per counter as a share of
	// the bucket's burst.
	AllowanceFraction float64
}

const (
	defaultLeaseWindow            = 250 * time.Millisecond
	defaultLeaseFraction          = 0.10
	defaultLeaseAllowanceFraction = 0.05

	// leaseTTL is how long unspent lease tokens stay usable. A token is
	// permission to admit a request now; holding it longer would let a
	// gateway save up a burst the shared bucket never granted.
	leaseTTL = time.Second
	// leaseRateTau is the time constant of a counter's request-rate EWMA.
	leaseRateTau = time.Second
	// leaseSweepEvery bounds how often idle lease states are dropped.
	leaseSweepEvery = 10 * time.Second
	// leaseEmptyMin and leaseEmptyMax bound how long a counter whose grant
	// came back empty answers 429 without a statement.
	leaseEmptyMin = 50 * time.Millisecond
	leaseEmptyMax = time.Second
	// leaseMaxWaits bounds how many lease statements one request waits for.
	leaseMaxWaits = 3
)

// Lease request results and token outcomes, the closed label sets of the
// leasing metrics.
const (
	LeaseResultGranted = "granted"
	LeaseResultPartial = "partial"
	LeaseResultEmpty   = "empty"
	LeaseResultFailed  = "failed"

	LeaseTokensGranted      = "granted"
	LeaseTokensSpent        = "spent"
	LeaseTokensAllowance    = "allowance"
	LeaseTokensOverAdmitted = "over_admitted"
	LeaseTokensExpired      = "expired"
	LeaseTokensReturned     = "returned"
)

// CentralLeaseBackend is the optional extension leasing uses to give unspent
// tokens back while the shared bucket is under pressure, so a customer pacing
// at the limit is not short-changed by tokens that expired on a gateway.
type CentralLeaseBackend interface {
	ReturnTokens(ctx context.Context, scope, subjectID, plan string, burst float64, n int) error
}

// LeaseObserver receives the leasing counters. The handler implements it
// over Prometheus; the limiter stays free of metrics dependencies.
type LeaseObserver interface {
	ObserveLeaseRequest(scope, result string)
	ObserveLeaseTokens(scope, outcome string, n int)
	ObserveRateLimitSyncConsult(scope string)
}

// leaseTable holds one Limiter's leases, one per central counter key.
type leaseTable struct {
	cfg      LeaseConfig
	observer LeaseObserver

	mu        sync.Mutex
	states    map[centralBatchKey]*leaseState
	lastSweep time.Time
	// calls tracks lease statements and returns in flight, for tests.
	calls sync.WaitGroup
}

// leaseState is one counter's lease on this gateway.
type leaseState struct {
	// tokens were debited from the shared counter and are not yet spent.
	tokens  int
	expires time.Time
	// owed counts allowance admissions not yet charged to the counter.
	owed int
	// remaining is the shared balance the last grant reported, -1 before
	// the first grant.
	remaining int
	// pressure: the last grant was partial or left little in the bucket, so
	// unspent tokens go back at expiry.
	pressure bool
	// emptyUntil: the last grant was empty; until then requests get 429
	// without a statement.
	emptyUntil time.Time
	// lastSize is how many tokens the last grant added; a refill starts in
	// the background once half of them are spent.
	lastSize int
	// rate is the counter's recent request rate at this gateway (EWMA).
	rate float64
	seen time.Time
	// rps and burst are the counter's policy as of its latest request.
	rps, burst float64
	waiters    int
	inflight   *leaseCall
}

// leaseCall is one lease statement in flight; requests that must wait for a
// lease wait on done.
type leaseCall struct {
	done chan struct{}
	err  error
}

// EnableLeasing turns on token leasing for central counters (ADR-1520). It
// takes effect only while the central backend can grant tokens in batches
// (CentralBatchBackend). Call before serving requests.
func (l *Limiter) EnableLeasing(cfg LeaseConfig, observer LeaseObserver) *Limiter {
	if cfg.Window <= 0 {
		cfg.Window = defaultLeaseWindow
	}
	if cfg.Fraction <= 0 || cfg.Fraction > 1 {
		cfg.Fraction = defaultLeaseFraction
	}
	if cfg.AllowanceFraction <= 0 || cfg.AllowanceFraction > 1 {
		cfg.AllowanceFraction = defaultLeaseAllowanceFraction
	}
	l.lease = &leaseTable{cfg: cfg, observer: observer, states: map[centralBatchKey]*leaseState{}}
	return l
}

// leaseConsume admits one request against a central counter from its lease.
// It returns the approximate shared balance for response headers. An error
// means the request is decided locally, as before leasing: the breaker is
// open and no lease is held, a lease statement failed, or a lease did not
// arrive in time.
func (l *Limiter) leaseConsume(ctx context.Context, batch CentralBatchBackend, scope, subjectID, plan string, rps, burst float64) (int, bool, error) {
	t := l.lease
	key := centralBatchKey{scope: scope, subjectID: subjectID, plan: plan}
	now := l.now()
	deadline := now.Add(centralConsultTimeout)
	waited, waits := false, 0

	t.mu.Lock()
	st := t.stateLocked(ctx, l, key, now)
	st.rps, st.burst = rps, burst
	st.observe(now)
	t.expireLocked(ctx, l, key, st, now)
	for {
		if st.tokens > 0 {
			st.tokens--
			t.count(scope, LeaseTokensSpent, 1)
			if st.inflight == nil && st.lastSize > 0 && 2*st.tokens <= st.lastSize {
				t.startLocked(l, ctx, batch, key, st)
			}
			balance := st.balance()
			t.mu.Unlock()
			return balance, true, nil
		}
		if now.Before(st.emptyUntil) {
			t.mu.Unlock()
			return 0, false, nil
		}
		if l.centralBreakerOpen(now) {
			t.mu.Unlock()
			return 0, false, errCentralBreakerOpen
		}
		if !waited && st.owed < t.allowance(burst) && (st.remaining < 0 || st.remaining >= t.allowance(burst)) {
			st.owed++
			t.count(scope, LeaseTokensAllowance, 1)
			t.startLocked(l, ctx, batch, key, st)
			balance := st.balance()
			t.mu.Unlock()
			return balance, true, nil
		}
		if !waited {
			waited = true
			if t.observer != nil {
				t.observer.ObserveRateLimitSyncConsult(scope)
			}
		}
		st.waiters++
		call := t.startLocked(l, ctx, batch, key, st)
		t.mu.Unlock()
		err := waitLease(ctx, call, deadline.Sub(l.now()))
		t.mu.Lock()
		st.waiters--
		if err != nil {
			t.mu.Unlock()
			return 0, false, err
		}
		now = l.now()
		t.expireLocked(ctx, l, key, st, now)
		// Each statement is sized for the requests waiting when it started;
		// one that arrived later may need the next. A few rounds settle it.
		waits++
		if st.tokens == 0 && (waits >= leaseMaxWaits || !now.Before(deadline)) {
			t.mu.Unlock()
			return 0, false, errCentralBatchWait
		}
	}
}

// waitLease waits for a lease statement, at most wait.
func waitLease(ctx context.Context, call *leaseCall, wait time.Duration) error {
	if wait <= 0 {
		return errCentralBatchWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errCentralBatchWait
	}
}

// centralBreakerOpen reports whether a recent central failure still has
// requests decided locally.
func (l *Limiter) centralBreakerOpen(now time.Time) bool {
	until := l.centralOpenUntil.Load()
	return until != 0 && now.UnixNano() < until
}

// startLocked starts a lease statement for key unless one is in flight. It
// asks for a lease sized to the counter's rate (or to the requests waiting,
// if more) plus what the allowance admitted.
func (t *leaseTable) startLocked(l *Limiter, ctx context.Context, batch CentralBatchBackend, key centralBatchKey, st *leaseState) *leaseCall {
	if st.inflight != nil {
		return st.inflight
	}
	size := max(t.leaseSize(st), st.waiters)
	call := &leaseCall{done: make(chan struct{})}
	st.inflight = call
	t.calls.Add(1)
	go t.run(l, context.WithoutCancel(ctx), batch, key, st, call, size, st.owed, st.rps, st.burst)
	return call
}

// run takes one lease. The statement answers for every request waiting on
// it, so it keeps the triggering request's values but not its cancellation.
func (t *leaseTable) run(l *Limiter, ctx context.Context, batch CentralBatchBackend, key centralBatchKey, st *leaseState, call *leaseCall, size, owed int, rps, burst float64) {
	defer t.calls.Done()
	leaseCtx, cancel := context.WithTimeout(ctx, centralConsultTimeout)
	granted, remaining, err := batch.ConsumeTokens(leaseCtx, key.scope, key.subjectID, key.plan, rps, burst, size+owed)
	cancel()
	now := l.now()

	t.mu.Lock()
	st.inflight = nil
	if err != nil {
		// The debt stays owed and is charged by the next lease.
		call.err = err
		t.mu.Unlock()
		l.centralOpenUntil.Store(now.Add(centralBreakerWindow).UnixNano())
		t.result(key.scope, LeaseResultFailed)
		close(call.done)
		return
	}
	l.centralOpenUntil.Store(0)
	// The grant pays the allowance's debt first. A debt it cannot cover was
	// admitted beyond the shared bucket; the counter is empty until it
	// should have refilled.
	paid := min(granted, owed)
	st.owed -= owed
	fresh := granted - paid
	if fresh > 0 {
		st.tokens += fresh
		st.expires = now.Add(leaseTTL)
	}
	st.lastSize = fresh
	st.remaining = remaining
	st.pressure = granted < size+owed || remaining < 2*size
	if granted < owed || granted == 0 {
		st.emptyUntil = now.Add(emptyBackoff(rps))
	}
	if granted > 0 {
		t.count(key.scope, LeaseTokensGranted, granted)
	}
	if owed > paid {
		t.count(key.scope, LeaseTokensOverAdmitted, owed-paid)
	}
	switch {
	case granted == 0:
		t.result(key.scope, LeaseResultEmpty)
	case granted < size+owed:
		t.result(key.scope, LeaseResultPartial)
	default:
		t.result(key.scope, LeaseResultGranted)
	}
	// Requests the allowance admitted while this statement ran are charged
	// at once rather than by the next request.
	if st.owed > 0 && !now.Before(st.emptyUntil) {
		t.startLocked(l, ctx, batch, key, st)
	}
	t.mu.Unlock()
	close(call.done)
}

// emptyBackoff is how long a counter whose grant came back empty answers 429
// without a statement: until one token should have refilled.
func emptyBackoff(rps float64) time.Duration {
	if rps <= 0 {
		return leaseEmptyMax
	}
	wait := time.Duration(float64(time.Second) / rps)
	return min(max(wait, leaseEmptyMin), leaseEmptyMax)
}

// expireLocked drops a lease's unspent tokens once they expire. While the
// shared bucket is under pressure they are given back; otherwise they are
// only counted, which bounds under-admission to one lease per gateway.
func (t *leaseTable) expireLocked(ctx context.Context, l *Limiter, key centralBatchKey, st *leaseState, now time.Time) {
	if st.tokens == 0 || now.Before(st.expires) {
		return
	}
	n := st.tokens
	st.tokens = 0
	returner, ok := l.central.(CentralLeaseBackend)
	if !st.pressure || !ok {
		t.count(key.scope, LeaseTokensExpired, n)
		return
	}
	t.calls.Add(1)
	go func(burst float64) {
		defer t.calls.Done()
		returnCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), centralConsultTimeout)
		defer cancel()
		if err := returner.ReturnTokens(returnCtx, key.scope, key.subjectID, key.plan, burst, n); err != nil {
			t.count(key.scope, LeaseTokensExpired, n)
			return
		}
		t.count(key.scope, LeaseTokensReturned, n)
	}(st.burst)
}

// stateLocked returns key's lease state, creating it, and now and then drops
// idle states so the table tracks only counters in use.
func (t *leaseTable) stateLocked(ctx context.Context, l *Limiter, key centralBatchKey, now time.Time) *leaseState {
	if now.Sub(t.lastSweep) >= leaseSweepEvery {
		t.lastSweep = now
		for k, st := range t.states {
			t.expireLocked(ctx, l, k, st, now)
			if st.idle(now) {
				delete(t.states, k)
			}
		}
	}
	st := t.states[key]
	if st == nil {
		st = &leaseState{remaining: -1}
		t.states[key] = st
	}
	return st
}

// idle reports whether dropping the state loses nothing: no tokens, debt or
// statement, and quiet long enough for the shared bucket to have refilled,
// so a new state's unknown balance is a full one.
func (st *leaseState) idle(now time.Time) bool {
	if st.tokens > 0 || st.owed > 0 || st.inflight != nil || now.Before(st.emptyUntil) {
		return false
	}
	quiet := leaseSweepEvery
	if st.rps > 0 {
		quiet = max(quiet, time.Duration(st.burst/st.rps*float64(time.Second)))
	}
	return now.Sub(st.seen) >= quiet
}

// observe folds one request into the counter's rate EWMA.
func (st *leaseState) observe(now time.Time) {
	if !st.seen.IsZero() {
		st.rate *= math.Exp(-now.Sub(st.seen).Seconds() / leaseRateTau.Seconds())
	}
	st.rate += 1 / leaseRateTau.Seconds()
	st.seen = now
}

// balance approximates the shared balance for response headers: the last
// grant's remainder plus this gateway's unspent lease. Other gateways may
// have spent tokens since.
func (st *leaseState) balance() int {
	if st.remaining < 0 {
		return max(int(st.burst)-st.owed, 0)
	}
	return st.remaining + st.tokens
}

// leaseSize is the next lease: about Window of the counter's recent traffic,
// at least one token, at most Fraction of the burst.
func (t *leaseTable) leaseSize(st *leaseState) int {
	ceiling := max(1, int(math.Floor(t.cfg.Fraction*st.burst)))
	size := int(math.Ceil(st.rate * t.cfg.Window.Seconds()))
	return min(max(size, 1), ceiling)
}

// allowance is how many requests one counter may admit on this gateway
// before their tokens are debited.
func (t *leaseTable) allowance(burst float64) int {
	return max(1, int(math.Floor(t.cfg.AllowanceFraction*burst)))
}

func (t *leaseTable) count(scope, outcome string, n int) {
	if t.observer != nil && n > 0 {
		t.observer.ObserveLeaseTokens(scope, outcome, n)
	}
}

func (t *leaseTable) result(scope, result string) {
	if t.observer != nil {
		t.observer.ObserveLeaseRequest(scope, result)
	}
}
