package gateway

import (
	"context"
	"errors"
	"sync"
	"time"
)

// WakeGate holds requests during a cold wake (spec §4.1 wake-blocking). When an
// app has no running instance, many simultaneous requests must trigger exactly
// ONE wake and all wait on it (single-flight per app), up to a cap; past the cap,
// or past the TTL, the caller returns 503 + Retry-After. This coalescing is what
// makes a burst to a parked app cost one restore, not N.
//
// Issue #675 / H2C multiplexing: WakeGate keys on appID alone (not on the
// connection), so N concurrent H2 streams on one unix-socket connection
// still coalesce into a single wake. The transport is irrelevant to the
// gate — HTTP/1.1, HTTP/2 cleartext, or in-process goroutines all hit
// the same single-flight map. Operators upgrading the public→internal
// hop to H2C (issue #675) do not lose coalescing semantics.
type WakeGate struct {
	mu       sync.Mutex
	inflight map[string]*wakeCall
	cap      int
	ttl      time.Duration
	// onChange is called whenever an entry's waiter count or completion state
	// changes, so the metrics layer can keep gateway_queue_depth current.
	// Optional; nil-safe at every call site.
	onChange func(appID, accountID string, depth int)
	// metrics observes how long each caller waited in the queue. Optional;
	// nil keeps the gate usable in unit tests that don't wire metrics.
	metrics *Metrics
}

// SetMetrics attaches a *Metrics so Wait can observe the per-caller wait
// duration (gateway_wake_queue_wait_seconds). Safe to call before serve
// starts; nil-safe on the gate's read path.
func (g *WakeGate) SetMetrics(m *Metrics) { g.metrics = m }

type wakeCall struct {
	done      chan struct{}
	err       error
	waiters   int
	completed bool // ensure() has returned; entry stays parked until drain
	// accountID is the resolved account that owns the app, captured
	// at Wait() entry. Used by onChange so the queue-depth gauge can
	// emit per-account series for FaasAlertPresetAnyFiringAccount
	// correlation. Empty string falls through to "__other__" at the
	// SetQueueDepth call site (bounded cardinality).
	accountID string
}

// ErrQueueFull is returned when the per-app waiter cap is exceeded (→ 503).
var ErrQueueFull = errors.New("gateway: wake queue full")

// ErrBootstrapAborted (ADR-098 C7) is returned when the detached-leader
// goroutine aborts under the bootstrap cap (queue empty AND no live
// instance AND plan.MaxMinInstances == 0). The followers see this
// rather than waiting for the gate TTL.
var ErrBootstrapAborted = errors.New("gateway: leader bootstrap aborted")

// NewWakeGate returns a gate with the given per-app waiter cap and wake TTL
// (spec §4.1: 512 / 30 s).
func NewWakeGate(capacity int, ttl time.Duration) *WakeGate {
	if capacity < 1 {
		capacity = 1
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &WakeGate{inflight: map[string]*wakeCall{}, cap: capacity, ttl: ttl}
}

// Wait ensures an instance for appID is awake, coalescing concurrent callers onto
// a single ensure() invocation. The leader runs ensure under the gate's TTL in a
// detached goroutine (so the triggering request cancelling doesn't abort the wake
// for other waiters); followers wait on the same result. Returns ErrQueueFull if
// the waiter cap is exceeded, the ensure error, or the caller's ctx error.
//
// shouldWake is consulted by the leader under the gate lock *after* becoming
// leader and *before* dispatching ensure. If shouldWake returns false (the app
// already has a ready instance via a peer's wake, observable via the Backend's
// Target), the leader short-circuits with err=nil and no ensure() call runs.
// This closes the race where a goroutine reaches Wait after the previous wake
// has set the instance running but before its old Target read sees it.
//
// shouldAbort (ADR-098 C7) is the optional detached-leader bootstrap
// cap predicate. The leader goroutine polls it on a 1s tick; when it
// returns true the goroutine aborts without calling ensure. The
// intended condition is: queue empty AND no live instance AND the
// app's plan has MaxMinInstances == 0. nil = leader never polls (the
// legacy behaviour). onAbort is the side-effect callback the leader
// fires before closing done — nil = no side effect.
//
// A completed but un-drained entry stays in the map until the last follower
// departs, so a follow-on request that arrives microseconds after ensure()
// returns cannot trigger a second wake (regression test:
// TestConcurrentColdRequestsCoalesceToOneWake).
//
// Queue-wait timing: we observe time.Since(start) for callers that
// actually waited (joined an in-flight call, or were the leader and
// dispatched ensure). ErrQueueFull and ctx.Err() returns are NOT
// recorded as queue-wait — they aren't wait, they're rejection or
// cancellation, and recording them as ~0ms observations would push
// the histogram's p95 toward zero during overload (the very signal
// the SLO dashboard needs to surface).
// Wait preserves the legacy gate contract for callers that do not have an
// app policy. Handler request paths should use WaitWithPolicy so the cap and
// wait budget are derived from the routed app's plan.
func (g *WakeGate) Wait(
	ctx context.Context,
	appID string,
	accountID string,
	shouldWake func() bool,
	ensure func(context.Context) error,
	shouldAbort func() bool,
	onAbort func(reason string),
) error {
	return g.WaitWithPolicy(ctx, appID, accountID, WakeAdmissionPolicy{
		MaxWaiters: g.cap,
		MaxWait:    g.ttl,
	}, shouldWake, ensure, shouldAbort, onAbort)
}

// WaitWithPolicy is Wait with an app-specific waiter cap and caller wait
// budget. The detached leader still uses the gate's lifecycle TTL so an
// individual client cancellation cannot orphan the shared wake; only the
// caller's wait is bounded by policy.MaxWait.
//
//nolint:contextcheck // the caller context is intentionally wrapped with the app's wait budget.
func (g *WakeGate) WaitWithPolicy(
	ctx context.Context,
	appID string,
	accountID string,
	policy WakeAdmissionPolicy,
	shouldWake func() bool,
	ensure func(context.Context) error,
	shouldAbort func() bool,
	onAbort func(reason string),
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	policy = policy.normalized(g.cap, g.ttl)
	start := time.Now()
	// observed is set true only on the paths where the caller actually
	// spent time in the queue. The deferred observer checks this flag.
	observed := false
	defer func() {
		if observed && g.metrics != nil {
			g.metrics.ObserveWakeQueueWait(time.Since(start))
		}
	}()

	g.mu.Lock()
	if call, ok := g.inflight[appID]; ok {
		if call.waiters >= policy.MaxWaiters {
			depth := call.waiters
			g.mu.Unlock()
			if g.onChange != nil {
				g.onChange(appID, call.accountID, depth)
			}
			return &WakeQueueFullError{
				Depth:      depth,
				Limit:      policy.MaxWaiters,
				RetryAfter: policy.MaxWait,
			}
		}
		call.waiters++
		depth := call.waiters
		g.mu.Unlock()
		if g.onChange != nil {
			g.onChange(appID, call.accountID, depth)
		}
		observed = true
		// Hold the followers' reference until await returns; release on exit.
		err := g.awaitWithPolicy(ctx, call, policy)
		g.release(appID, call)
		// ctx.Err() is also "didn't wait for an ensure result" — skip
		// the metric on cancellation so a hung client's cancellation
		// doesn't pollute the wake-latency histogram.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			observed = false
		}
		return err
	}

	call := &wakeCall{done: make(chan struct{}), waiters: 1, accountID: accountID}
	g.inflight[appID] = call
	g.mu.Unlock()
	if g.onChange != nil {
		g.onChange(appID, accountID, 1)
	}

	// Leader-only: skip the wake if the Backend already has a ready instance
	// (a peer's wake just finished and we observe it here). shouldWake runs
	// synchronously under no lock; the Backend must serialize state itself.
	if !shouldWake() {
		g.complete(appID, call, nil)
		// Treat the leader itself as a waiter so release() can drop the
		// entry; a follower that arrives later increments waiters to 2 then
		// drops to 1 after await; both leader and follower call release().
		_ = g.awaitWithPolicy(ctx, call, policy)
		g.release(appID, call)
		return nil
	}

	//nolint:contextcheck // leader goroutine deliberately detaches from the
	// caller's ctx via context.Background() — the wake must outlive the
	// triggering request so other queued waiters get the same instance.
	// This is the load-bearing single-flight coalescing invariant (spec §4.1).
	//
	// ADR-098 C7: bootstrap-cap abort. When shouldAbort is non-nil, the
	// leader races ensure() against a poller that closes abortCh when
	// shouldAbort becomes true. The poller fires on a 1s ticker. The
	// intended condition (coldStart) is: queue empty AND no live instance
	// AND plan.MaxMinInstances == 0. That predicate is true exactly when
	// the leader's caller has bailed (waiters dropped to 0) and the
	// wake hasn't produced a target yet — so the abort is for orphaned
	// wakes, not for the leader's own initial entry.
	//
	// Race semantics:
	//   - abortCh closes first → leader writes ErrBootstrapAborted, closes
	//     done. Followers see ErrBootstrapAborted via await.
	//   - ensure() returns first → leader writes ensure's err, closes done.
	//     Poller exits on ectx.Done().
	//
	// Exactly one close(call.done) — write is guarded by `g.mu`. The
	// orphan-detach invariant (the leader's caller-ctx doesn't kill the
	// wake) is preserved; the abort is opt-in via shouldAbort.
	//
	// ADR-093 cross-reference: this detachment is intentionally NOT
	// changed by the end-to-end request-budget work. Reusing the
	// inbound budget here would break coalescing — a client that
	// disconnects mid-wake must not abort the wake for the rest of
	// the waiters. The end-to-end budget clamps only the WAITER's
	// ctx (the ctx the follower passes to g.await), which causes
	// the follower's own ctx to fire on budget expiry (correct).
	// The leader's detached ctx continues to drive the wake to
	// completion. See ADR-093 §Consequences.
	go func() {
		ectx, cancel := context.WithTimeout(context.Background(), g.ttl)
		defer cancel()

		// Optional bootstrap-cap poller. Lifecycle:
		//   - spawned only when shouldAbort != nil
		//   - fires at most once (close(abortCh)) on the first tick
		//     where shouldAbort() returns true
		//   - exits on ectx.Done() (TTL hit) before firing
		// This is the only goroutine that closes abortCh.
		var abortCh chan struct{}
		var pollerDone chan struct{}
		if shouldAbort != nil {
			abortCh = make(chan struct{})
			pollerDone = make(chan struct{})
			go func() {
				defer close(pollerDone)
				ticker := time.NewTicker(1 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if shouldAbort() {
							close(abortCh)
							return
						}
					case <-ectx.Done():
						return
					}
				}
			}()
		}

		// Race ensure against abortCh. Whichever fires first owns
		// the close(call.done).
		ensureDone := make(chan error, 1)
		go func() {
			ensureDone <- ensure(ectx)
		}()

		select {
		case err := <-ensureDone:
			// Poller may still be running (it has its own ticker
			// and exits on ectx.Done()). We DON'T join it here —
			// the gate contract is "done is closed when the
			// leader has the result"; the poller is an internal
			// orphan-detector, not a gate observer. Reap at
			// ectx.Done() via a separate drain below.
			g.complete(appID, call, err)
		case <-abortCh:
			if onAbort != nil {
				onAbort("queue_empty_no_instance")
			}
			// The ensure goroutine is still running; we accept
			// the orphan: it'll exit on ectx.Done() (the TTL).
			// The follower-facing contract is satisfied.
			<-pollerDone
			g.complete(appID, call, ErrBootstrapAborted)
		case <-ectx.Done():
			// TTL hit before either fired. The poller has exited
			// on the same ectx; reap it. ensure is still running
			// (it's blocked on ectx from inside the user func);
			// accept the orphan.
			if pollerDone != nil {
				<-pollerDone
			}
			err := <-ensureDone
			g.complete(appID, call, err)
		}
	}()

	observed = true
	err := g.awaitWithPolicy(ctx, call, policy)
	g.release(appID, call)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		observed = false
	}
	return err
}

// complete publishes the result and drops a wake whose waiters already left.
// release handles the opposite ordering: completion before the last departure.
// Keeping both under mu prevents a later request from inheriting an orphaned
// wake's result while preserving coalescing until its remaining waiters drain.
func (g *WakeGate) complete(appID string, call *wakeCall, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	call.err = err
	call.completed = true
	if call.waiters == 0 && g.inflight[appID] == call {
		delete(g.inflight, appID)
	}
	close(call.done)
}

// InflightWaiters returns the current waiter count for appID (0 if none). For
// the gateway_queue_depth metric and tests.
func (g *WakeGate) InflightWaiters(appID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if call, ok := g.inflight[appID]; ok {
		return call.waiters
	}
	return 0
}

// WakeInProgress reports whether a wake generation still exists for appID.
// A generation can have zero waiters after the triggering browser has received
// its wake page while the detached leader continues booting, so this is
// intentionally distinct from InflightWaiters.
func (g *WakeGate) WakeInProgress(appID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	call, ok := g.inflight[appID]
	return ok && !call.completed
}

// InflightFollowers returns the count of *followers* (waiters minus
// the leader's own registration) for appID. The leader is created
// with waiters=1, so this returns 0 when only the leader is alive —
// which is the signal the bootstrap-cap abort predicate needs:
//
//	"queue empty AND no live instance AND no plan floor"
//
// Here "queue empty" means "no followers", not "the leader also
// gone" — the leader is necessarily alive at this point
// (otherwise no bootstrap is happening). ADR-098 C7 review fix:
// without the -1, the predicate is unsatisfiable while the leader's
// caller awaits done. Returns 0 if no entry.
func (g *WakeGate) InflightFollowers(appID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if call, ok := g.inflight[appID]; ok {
		if call.waiters > 0 {
			return call.waiters - 1
		}
		return 0
	}
	return 0
}

// release decrements the waiter count and, if the leader's ensure has finished
// and no other follower is still waiting, removes the entry. Done on the
// leader's path AND every follower's path so a completed wake is observable to
// every concurrent caller regardless of arrival order.
func (g *WakeGate) release(appID string, call *wakeCall) {
	g.mu.Lock()
	defer g.mu.Unlock()
	call.waiters--
	if call.completed && call.waiters == 0 {
		delete(g.inflight, appID)
	}
	if g.onChange != nil {
		depth := 0
		acct := call.accountID
		if c, ok := g.inflight[appID]; ok {
			depth = c.waiters
			acct = c.accountID
		}
		g.onChange(appID, acct, depth)
	}
}

func (g *WakeGate) await(ctx context.Context, call *wakeCall) error {
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *WakeGate) awaitWithPolicy(ctx context.Context, call *wakeCall, policy WakeAdmissionPolicy) error {
	waitCtx, cancel := context.WithTimeout(ctx, policy.MaxWait)
	defer cancel()
	err := g.await(waitCtx, call)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return &WakeQueueWaitTimeoutError{RetryAfter: policy.MaxWait}
	}
	return err
}
