package gateway

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
//
// Route priority (ADR-20261018181846): with a classifier, a full queue admits
// a request that outranks a waiter by turning away the newest waiter of the
// lowest lower class, and a successful wake releases critical waiters first,
// then normal, then bulk, RoutePriorityWakeReleaseStagger apart.
type WakeGate struct {
	mu       sync.Mutex
	inflight map[string]*wakeCall
	cap      int
	ttl      time.Duration
	// leaderFloor is the least time the detached leader's ensure runs,
	// whatever the TTL and the app's wait budget. The handler sets it to
	// schedd's leader budget; zero keeps unit-test gates on their TTL.
	leaderFloor time.Duration
	// onChange is called whenever an entry's waiter count or completion state
	// changes, so the metrics layer can keep gateway_queue_depth current.
	// Optional; nil-safe at every call site.
	onChange func(appID, accountID string, depth int)
	// onPlanChange mirrors onChange with the resolved plan label so metrics can
	// expose the issue-1060 gateway_wake_queue_depth{app,plan} series.
	onPlanChange func(appID, plan string, depth int)
	// metrics observes how long each caller waited in the queue. Optional;
	// nil keeps the gate usable in unit tests that don't wire metrics.
	metrics *Metrics
	// classifier orders waiters by route priority. nil keeps the gate
	// first-come, first-served. releaseStagger separates the release of
	// each lower class.
	classifier     wakeClassifier
	releaseStagger time.Duration
}

// wakeClassifier classifies wake waiters by route priority. With wait false
// it must not block, and reports false when it cannot classify yet.
type wakeClassifier interface {
	classifyWake(ctx context.Context, appID, accountID string, reqs []routePriorityRequest, wait bool) ([]routePriority, bool)
}

// wakeWaiter is one request held behind a wake.
type wakeWaiter struct {
	req    routePriorityRequest
	leader bool
	// released is closed when the waiter may proceed after a successful
	// wake; displaced when a higher class took its place.
	released     chan struct{}
	displaced    chan struct{}
	displacedErr error
}

func newWakeWaiter(ctx context.Context) *wakeWaiter {
	req, _ := ctx.Value(routePriorityRequestKey{}).(routePriorityRequest)
	return &wakeWaiter{req: req, released: make(chan struct{}), displaced: make(chan struct{})}
}

// SetMetrics attaches a *Metrics so Wait can observe the per-caller wait
// duration (gateway_wake_queue_wait_seconds). Safe to call before serve
// starts; nil-safe on the gate's read path.
func (g *WakeGate) SetMetrics(m *Metrics) { g.metrics = m }

type wakeCall struct {
	target    string
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
	plan      string
	// queue holds every waiter, the leader first; len(queue) == waiters.
	queue []*wakeWaiter
}

// ErrQueueFull is returned when the per-app waiter cap is exceeded (→ 503).
var ErrQueueFull = errors.New("gateway: wake queue full")

// A caller queued behind another cohort must retry its own admission, rather
// than treating that cohort's success or failure as its own result.
var errWakeTargetChanged = errors.New("gateway: another deployment wake completed")

type wakeTargetKey struct{}

// ErrBootstrapAborted (ADR-098 C7) is returned when the detached-leader
// goroutine aborts under the bootstrap cap (queue empty AND no live
// instance AND plan.MaxMinInstances == 0). The followers see this
// rather than waiting for the leader's budget.
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
	return &WakeGate{inflight: map[string]*wakeCall{}, cap: capacity, ttl: ttl, releaseStagger: api.RoutePriorityWakeReleaseStagger}
}

// Wait ensures an instance for appID is awake, coalescing concurrent callers onto
// a single ensure() invocation. The leader runs ensure under leaderBudget in a
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
// budget. The detached leader runs for leaderBudget so an individual client
// cancellation cannot orphan the shared wake; only the caller's wait is
// bounded by policy.MaxWait.
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

	w := newWakeWaiter(ctx)
	g.mu.Lock()
	target, _ := ctx.Value(wakeTargetKey{}).(string)
	if call, ok := g.inflight[appID]; ok && call.completed && call.target != target {
		delete(g.inflight, appID)
	}
	if call, ok := g.inflight[appID]; ok {
		if call.waiters >= policy.MaxWaiters && !g.displaceFor(ctx, appID, call, w, policy) {
			depth := call.waiters
			g.mu.Unlock()
			g.notifyChange(appID, call.accountID, call.plan, depth)
			return &WakeQueueFullError{
				Depth:      depth,
				Limit:      policy.MaxWaiters,
				RetryAfter: policy.MaxWait,
			}
		}
		g.join(call, w)
		depth := call.waiters
		g.mu.Unlock()
		g.notifyChange(appID, call.accountID, call.plan, depth)
		if depth == 2 && g.classifier != nil {
			// A second request is waiting: load the app's priorities while
			// the instance boots, so the release can order the waiters.
			g.classifier.classifyWake(ctx, appID, call.accountID, nil, false)
		}
		observed = true
		// Hold the followers' reference until await returns; release on exit.
		err := g.awaitWithPolicy(ctx, call, w, policy)
		g.release(appID, call, w)
		// ctx.Err() is also "didn't wait for an ensure result" — skip
		// the metric on cancellation so a hung client's cancellation
		// doesn't pollute the wake-latency histogram.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			observed = false
		}
		var waitTimeout *WakeQueueWaitTimeoutError
		if target != "" && call.target != target && ctx.Err() == nil && !errors.As(err, &waitTimeout) {
			return errWakeTargetChanged
		}
		return err
	}

	w.leader = true
	call := &wakeCall{done: make(chan struct{}), waiters: 1, accountID: accountID, plan: policy.Plan, target: target, queue: []*wakeWaiter{w}}
	g.inflight[appID] = call
	g.mu.Unlock()
	g.notifyChange(appID, accountID, policy.Plan, 1)

	// Leader-only: skip the wake if the Backend already has a ready instance
	// (a peer's wake just finished and we observe it here). shouldWake runs
	// synchronously under no lock; the Backend must serialize state itself.
	if !shouldWake() {
		g.complete(appID, call, nil)
		// Treat the leader itself as a waiter so release() can drop the
		// entry; a follower that arrives later increments waiters to 2 then
		// drops to 1 after await; both leader and follower call release().
		_ = g.awaitWithPolicy(ctx, call, w, policy)
		g.release(appID, call, w)
		return nil
	}

	//nolint:contextcheck // leader goroutine deliberately detaches cancellation
	// and deadlines from the caller while retaining request-scoped values. The
	// wake must outlive the triggering request so other queued waiters get the
	// same instance, but its correlation envelope must survive admission.
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
	// ADR-093 cross-reference: the guest execution budget starts after wake,
	// so this waiter is bounded by WakeAdmissionPolicy.MaxWait instead. Reusing
	// client cancellation for the leader would still break coalescing: a client
	// that disconnects mid-wake must not abort the wake for other waiters. The
	// leader's detached context continues for leaderBudget.
	detached := context.WithoutCancel(ctx)
	leaderBudget := g.leaderBudget(policy)
	go func() {
		ectx, cancel := context.WithTimeout(detached, leaderBudget)
		defer cancel()

		// Optional bootstrap-cap poller. Lifecycle:
		//   - spawned only when shouldAbort != nil
		//   - fires at most once (close(abortCh)) on the first tick
		//     where shouldAbort() returns true
		//   - exits on ectx.Done() (leaderBudget hit) before firing
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
			// the orphan: it'll exit on ectx.Done() (leaderBudget).
			// The follower-facing contract is satisfied.
			<-pollerDone
			g.complete(appID, call, ErrBootstrapAborted)
		case <-ectx.Done():
			// leaderBudget hit before either fired. The poller has exited
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
	err := g.awaitWithPolicy(ctx, call, w, policy)
	g.release(appID, call, w)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		observed = false
	}
	return err
}

// leaderBudget bounds the detached leader's ensure. It covers the app's own
// wait budget, which wake_max_queue_wait_seconds and max_queue_wait_ms may set
// above the TTL, and leaderFloor. Hunt 22 H22-1: the leader stopped at the
// TTL, so those budgets could not take effect, and a wake schedd finished in
// 30 to 45 s answered every waiter 503 instead of "wake in progress".
func (g *WakeGate) leaderBudget(policy WakeAdmissionPolicy) time.Duration {
	return max(g.ttl, policy.MaxWait, g.leaderFloor)
}

// join adds w to call. A waiter that arrives after the wake finished is
// released at once.
func (g *WakeGate) join(call *wakeCall, w *wakeWaiter) {
	call.waiters++
	call.queue = append(call.queue, w)
	if call.completed {
		close(w.released)
	}
}

// displaceFor is called with g.mu held on a full queue and returns with it
// held. It classifies the newcomer and the waiters, which may read the app's
// priorities, and turns away the newest waiter of the lowest class below the
// newcomer's; the leader never moves. It reports whether w may join.
func (g *WakeGate) displaceFor(ctx context.Context, appID string, call *wakeCall, w *wakeWaiter, policy WakeAdmissionPolicy) bool {
	if g.classifier == nil || call.completed {
		return false
	}
	waiting := slices.Clone(call.queue)
	reqs := make([]routePriorityRequest, 0, len(waiting)+1)
	for _, waiter := range waiting {
		reqs = append(reqs, waiter.req)
	}
	reqs = append(reqs, w.req)
	g.mu.Unlock()
	classes, ok := g.classifier.classifyWake(ctx, appID, call.accountID, reqs, true)
	g.mu.Lock()
	if g.inflight[appID] != call || call.completed {
		return false
	}
	if call.waiters < policy.MaxWaiters {
		return true // a waiter left while the priorities were read
	}
	if !ok || len(classes) != len(reqs) {
		return false
	}
	class := classes[len(classes)-1]
	victim := -1
	for i := len(waiting) - 1; i >= 0; i-- {
		if waiting[i].leader || classes[i] <= class || !slices.Contains(call.queue, waiting[i]) {
			continue
		}
		if victim < 0 || classes[i] > classes[victim] {
			victim = i
		}
	}
	if victim < 0 {
		g.metrics.IncRoutePriorityWake(appID, class.String(), "rejected")
		return false
	}
	displaced := waiting[victim]
	call.queue = slices.DeleteFunc(call.queue, func(q *wakeWaiter) bool { return q == displaced })
	call.waiters--
	displaced.displacedErr = &WakeQueueFullError{Depth: call.waiters + 1, Limit: policy.MaxWaiters, RetryAfter: policy.MaxWait}
	close(displaced.displaced)
	g.metrics.IncRoutePriorityWake(appID, classes[victim].String(), "displaced")
	g.metrics.IncRoutePriorityWake(appID, class.String(), "displacing")
	return true
}

// complete publishes the result and drops a wake whose waiters already left.
// release handles the opposite ordering: completion before the last departure.
// Keeping both under mu prevents a later request from inheriting an orphaned
// wake's result while preserving coalescing until its remaining waiters drain.
func (g *WakeGate) complete(appID string, call *wakeCall, err error) {
	g.mu.Lock()
	call.err = err
	call.completed = true
	if call.waiters == 0 && g.inflight[appID] == call {
		delete(g.inflight, appID)
	}
	waiting := slices.Clone(call.queue)
	close(call.done)
	g.mu.Unlock()
	g.releaseWaiters(appID, call.accountID, waiting, err)
}

// releaseWaiters lets the waiters of a finished wake proceed, one route
// priority class at a time, releaseStagger apart.
func (g *WakeGate) releaseWaiters(appID, accountID string, waiting []*wakeWaiter, err error) {
	for i, group := range g.releaseOrder(appID, accountID, waiting, err) {
		open := func() {
			for _, w := range group {
				close(w.released)
			}
		}
		if i == 0 {
			open()
			continue
		}
		time.AfterFunc(time.Duration(i)*g.releaseStagger, open)
	}
}

// releaseOrder groups waiters by class, critical first. A failed wake, a lone
// waiter, or priorities that are not loaded yet release everyone together.
func (g *WakeGate) releaseOrder(appID, accountID string, waiting []*wakeWaiter, err error) [][]*wakeWaiter {
	if err != nil || len(waiting) < 2 || g.classifier == nil {
		return [][]*wakeWaiter{waiting}
	}
	reqs := make([]routePriorityRequest, len(waiting))
	for i, w := range waiting {
		reqs[i] = w.req
	}
	classes, ok := g.classifier.classifyWake(context.Background(), appID, accountID, reqs, false)
	if !ok || len(classes) != len(waiting) {
		return [][]*wakeWaiter{waiting}
	}
	var byClass [routePriorityBulk + 1][]*wakeWaiter
	for i, w := range waiting {
		class := min(max(classes[i], routePriorityCritical), routePriorityBulk)
		byClass[class] = append(byClass[class], w)
	}
	var out [][]*wakeWaiter
	for class, group := range byClass {
		if len(group) == 0 {
			continue
		}
		if len(out) > 0 {
			for range group {
				g.metrics.IncRoutePriorityWake(appID, routePriority(class).String(), "released_later")
			}
		}
		out = append(out, group)
	}
	return out
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

// Inflight reports whether appID currently has a wake coordinated by this
// gate. Unlike InflightWaiters, this remains true while a detached wake is
// finishing after its last request has gone away, which lets cache-aware
// callers distinguish an active wake from an ordinary cold miss.
func (g *WakeGate) Inflight(appID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.inflight[appID]
	return ok
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
func (g *WakeGate) release(appID string, call *wakeCall, w *wakeWaiter) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !slices.Contains(call.queue, w) {
		return // displaced: its place was already given up
	}
	call.queue = slices.DeleteFunc(call.queue, func(q *wakeWaiter) bool { return q == w })
	call.waiters--
	if call.completed && call.waiters == 0 && g.inflight[appID] == call {
		delete(g.inflight, appID)
	}
	depth := 0
	acct := call.accountID
	plan := call.plan
	if c, ok := g.inflight[appID]; ok {
		depth = c.waiters
		acct = c.accountID
		plan = c.plan
	}
	g.notifyChange(appID, acct, plan, depth)
}

func (g *WakeGate) notifyChange(appID, accountID, plan string, depth int) {
	if g.onChange != nil {
		g.onChange(appID, accountID, depth)
	}
	if g.onPlanChange != nil {
		g.onPlanChange(appID, plan, depth)
	}
}

func (g *WakeGate) await(ctx context.Context, call *wakeCall, w *wakeWaiter) error {
	// Prefer caller cancellation when both signals are already ready. A bare
	// select chooses randomly in that case, which can report a completed wake
	// (and record queue latency) for a request that was cancelled before it
	// entered the gate.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-call.done:
	case <-w.displaced:
		return w.displacedErr
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-w.displaced:
		return w.displacedErr // turned away just before the wake finished
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if call.err != nil {
		return call.err
	}
	// A successful wake releases higher route priority classes first.
	select {
	case <-w.released:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *WakeGate) awaitWithPolicy(ctx context.Context, call *wakeCall, w *wakeWaiter, policy WakeAdmissionPolicy) error {
	waitCtx, cancel := context.WithTimeout(ctx, policy.MaxWait)
	defer cancel()
	err := g.await(waitCtx, call, w)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return &WakeQueueWaitTimeoutError{RetryAfter: policy.MaxWait}
	}
	return err
}
