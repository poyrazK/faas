package sched

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/dispatch"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compile-time guarantee the unified invocations drain depends on
// the pkg/dispatch contract (ADR-134 §6.7). The drain consumes
// RetryPolicy, DeadlinePolicy, and dispatch.Job once per-row
// fields land on the invocations table in PR-B; today only the
// type dependency is wired so a future API change to pkg/dispatch
// surfaces in this file's compile, not at runtime.
var _ dispatch.JobKind = dispatch.JobKindInvocation

// ErrPermanentInvoke is the sentinel the gateway returns when an
// envelope is permanently undeliverable (4xx — bad payload, app
// deleted, no such method). The drain maps this to state='failed'
// (retryAfter=0) instead of bouncing the row back to pending; a
// retried bad payload just keeps failing.
var ErrPermanentInvoke = errors.New("sched: permanent invoke error")

// ErrPermanentWake is the sentinel for wake errors that the drain
// will never recover from (no such app, app PARKED, account deleted).
// Surfaced by the engine's Wake implementation; the drain short-circuits
// to state='failed'.
var ErrPermanentWake = errors.New("sched: permanent wake error")

// ErrDispatchTimeout is the sentinel a wake or invoke path wraps when
// the failure was a blown deadline rather than a rejection — a gateway
// 504, an expired claim lease, or a context deadline. It exists purely
// so the durable outcome (issue #791, migrations/00166) can say
// "timeout" instead of the undifferentiated "failed", which is what
// makes the per-cron run history able to render a distinct timeout
// row. It says nothing about retryability: a timeout may be transient
// or permanent, and that decision stays with ErrPermanentWake /
// ErrPermanentInvoke.
var ErrDispatchTimeout = errors.New("sched: dispatch timeout")

// failOutcome classifies a dispatch error for the durable outcome
// column. Deliberately sentinel-based (errors.Is) rather than
// string-matching the error text: last_error is operator-facing and
// unversioned, and the two producers already vary their prefixes
// ("wake: " / "invoke: ").
//
// context.DeadlineExceeded is folded in because both the engine's
// Wake and the gateway's Invoke surface a blown context that way
// without necessarily wrapping our own sentinel.
func failOutcome(err error) state.FailOption {
	if errors.Is(err, ErrDispatchTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return state.WithOutcome(state.OutcomeTimeout)
	}
	return state.WithOutcome(state.OutcomeFailed)
}

// Drain is the Move 1 event-shaped scheduler. It walks due rows from
// the unified invocations table (async_invoke / queue / delayed_task /
// cron) and dispatches them through the wake gate. Cron rows arrive
// via the cron loop's EnqueueInvocation call; the drain itself only
// owns the state-machine transitions pending → dispatching →
// completed/failed.
//
// Architecture notes (matching the §14 ownership table):
//
//   - schedd is the single writer to instances (CLAUDE.md); the drain
//     reuses engine.Wake so the same wake gate / admit path that
//     customer traffic uses applies (this is the entire point of
//     "event-shaped traffic reaches the same wake gate as HTTP
//     traffic" — no second admission policy).
//   - The drain is fan-in for four `source` values; they share one
//     hot index (invocations_due_idx on due_at WHERE state='pending')
//     and one drain tick (1s safety + pg_notify invocation_due).
//   - Always-Wake (idempotent): every dispatch calls engine.Wake
//     even if an instance is already RUNNING. Today this costs a
//     round-trip to schedd's Wake RPC; if profiling shows it on a
//     hot path Move 2 can short-circuit using ListAppsForWake +
//     RunningInstanceForApp. For Move 1 we pay the round-trip for
//     correctness.
//   - Cap re-checks: row may sit in pending for a long time; if the
//     customer's plan changed (e.g. downgrade), CountPendingInvocations
//     re-checks the cap right before claiming. The drain never trusts
//     apid's prior gate.
//   - No new daemon: drains live inside cmd/schedd. The schedd main
//     goroutine subscribes to invocation_due + runDrainTick (1s).
type Drain struct {
	store     state.Store
	engine    *Engine
	gateway   GatewaySynth
	notifier  Notifier
	log       *slog.Logger
	now       func() time.Time
	batchSize int
	// wakeLeaseSeconds is the lease a claimed invocation holds. The
	// drain races claim → wake → invoke → complete inside this window.
	// 60s is generous (the Wake+Invoke flow normally completes in well
	// under 1s when the instance is RUNNING), but padding survives
	// slow SnapRestore cold boots on Scale plans.
	wakeLeaseSeconds int
	// retryAfterSeconds is what transient errors push due_at forward
	// by. 5s is short enough that user-visible delay doesn't drift
	// far past the SLO; long enough that the drain doesn't fan out a
	// hot loop on a stuck backend.
	retryAfterSeconds int
	// dispatchConcurrency bounds the number of non-queue invocations
	// dispatched in parallel for one drain tick. Queue-source rows stay
	// serial per app so the FIFO contract is preserved.
	dispatchConcurrency int
	// accts caches Active(account_id) lookups for the suspended-skip
	// check (Move 2). One entry per account; the 5s TTL collapses the
	// 64-row batch's per-row AccountByID into ≤0.2 RPS at Meta's
	// "everyone suspends at once" worst case.
	accts *acctCache
}

// DefaultDrainDispatchConcurrency is deliberately small: it removes the
// serial scheduler bottleneck without allowing one noisy account to consume
// the entire schedd process. Operators can tune it with
// FAAS_SCHEDD_INVOCATION_DISPATCH_CONCURRENCY.
const DefaultDrainDispatchConcurrency = 8

// expiredInvocationReclaimer is implemented by the durable stores. Keeping
// this as a narrow optional interface preserves the broad state.Store test
// seam while making lease recovery part of the production drain contract.
type expiredInvocationReclaimer interface {
	RequeueExpiredInvocations(context.Context, time.Time, int) (int, error)
}

// acctCache is a tiny TTL map. Move 2 hardware-plan: the suspended-account
// check needs to avoid 64 AccountByID round-trips per batch under steady
// load. Five seconds is the natural window — same order as the per-app
// plan cache (cmd/apid uses a 5 s memo) and short enough that a
// reactivation lands within a SLO.
type acctCache struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]acctCacheEntry
}

type acctCacheEntry struct {
	active    bool
	expiresAt time.Time
}

func newAcctCache(now func() time.Time) *acctCache {
	return &acctCache{now: now, m: map[string]acctCacheEntry{}}
}

func (c *acctCache) get(accountID string) (acctCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[accountID]
	if !ok || !c.now().Before(e.expiresAt) {
		return acctCacheEntry{}, false
	}
	return e, true
}

func (c *acctCache) put(accountID string, active bool, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[accountID] = acctCacheEntry{active: active, expiresAt: expiresAt}
}

// DrainOption configures the Drain without leaking every field to the
// caller; cmd/schedd only sets the ones the production code cares about.
type DrainOption func(*Drain)

func WithDrainBatchSize(n int) DrainOption  { return func(d *Drain) { d.batchSize = n } }
func WithDrainWakeLease(s int) DrainOption  { return func(d *Drain) { d.wakeLeaseSeconds = s } }
func WithDrainRetryAfter(s int) DrainOption { return func(d *Drain) { d.retryAfterSeconds = s } }
func WithDrainDispatchConcurrency(n int) DrainOption {
	return func(d *Drain) { d.dispatchConcurrency = n }
}
func WithDrainNow(now func() time.Time) DrainOption    { return func(d *Drain) { d.now = now } }
func WithDrainLogger(l *slog.Logger) DrainOption       { return func(d *Drain) { d.log = l } }
func WithDrainGatewaySynth(g GatewaySynth) DrainOption { return func(d *Drain) { d.gateway = g } }
func WithDrainNotifier(n Notifier) DrainOption         { return func(d *Drain) { d.notifier = n } }

// NewDrain wires the dependencies. Defaults are conservative: 64-batch
// per tick, 60s wake lease, 5s retry-after, real clock.
func NewDrain(store state.Store, engine *Engine, opts ...DrainOption) *Drain {
	d := &Drain{
		store:               store,
		engine:              engine,
		now:                 time.Now,
		batchSize:           64,
		wakeLeaseSeconds:    60,
		retryAfterSeconds:   5,
		dispatchConcurrency: DefaultDrainDispatchConcurrency,
		log:                 slog.Default(),
	}
	for _, o := range opts {
		o(d)
	}
	// acctCache is wired last so it adopts the Drain's now() injection
	// (tests use WithDrainNow to exercise TTL boundaries). Move 2.
	d.accts = newAcctCache(d.now)
	return d
}

// Run blocks until ctx is cancelled. It listens on notif for
// invocation_due events (the drain's hot wake) and a 1s safety ticker
// (in case a notify is dropped or the LISTEN connection hiccups — pg_notify
// is fire-and-forget over a single LISTEN session).
//
// Both paths call tick(). tick() drains the due queue in 64-row batches
// until either the slice is shorter than the batch or no rows remain.
func (d *Drain) Run(ctx context.Context, notif <-chan db.Notification) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				// Channel closed: pg_notify connection dropped. The
				// reconnect wrapper in cmd/schedd re-opens the
				// channel; here we just continue on tick + the
				// reconnected channel's first emission.
				continue
			}
			if n.Channel != db.NotifyInvocationDue {
				continue
			}
			d.Tick(ctx)
		case <-ticker.C:
			d.Tick(ctx)
		}
	}
}

// Tick is the per-cycle drain walk. Public so tests can drive it
// without spinning Run().
func (d *Drain) Tick(ctx context.Context) {
	// A crashed schedd/gateway can leave a row in dispatching after its
	// wake/invoke lease expires. Requeue those rows before reading pending
	// work so the next scheduler tick can make progress. The store update is
	// conditional on the expired lease and bounded to the same batch size as
	// the hot queue walk.
	if reclaimer, ok := d.store.(expiredInvocationReclaimer); ok {
		reclaimed, err := reclaimer.RequeueExpiredInvocations(ctx, d.now(), d.batchSize)
		if err != nil {
			d.log.Warn("drain: reclaim expired invocations", "err", err)
		} else if reclaimed > 0 {
			d.log.Warn("drain: reclaimed expired invocations", "count", reclaimed)
		}
	}
	for {
		rows, err := d.store.ListDueInvocations(ctx, d.now(), d.batchSize)
		if err != nil {
			d.log.Warn("drain: list-due", "err", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		// Tenant-fairness: bucket by app so a 1,000-row queue for
		// one app doesn't starve a 1-row queue for another (the
		// batch is bounded at 64 so contention is small, but the
		// round-robin protects long-tail apps even within one tick).
		byApp := map[string][]state.Invocation{}
		order := []string{}
		for _, r := range rows {
			if _, seen := byApp[r.AppID]; !seen {
				order = append(order, r.AppID)
			}
			byApp[r.AppID] = append(byApp[r.AppID], r)
		}
		parallelRows := make([]state.Invocation, 0, len(rows))
		queueRowsByApp := make(map[string][]state.Invocation)
		for _, appID := range order {
			for _, inv := range byApp[appID] {
				if inv.Source == state.InvocationQueue {
					queueRowsByApp[appID] = append(queueRowsByApp[appID], inv)
					continue
				}
				parallelRows = append(parallelRows, inv)
			}
		}
		d.dispatchParallel(ctx, parallelRows)
		// Queue rows remain serial per app so the FIFO contract is
		// preserved. Other event sources use the bounded pool above.
		for _, appID := range order {
			for _, inv := range queueRowsByApp[appID] {
				d.dispatchOne(ctx, inv)
			}
		}
		if len(rows) < d.batchSize {
			return
		}
	}
}

// dispatchParallel runs non-FIFO invocation sources through a bounded worker
// pool. ClaimInvocationWithCap is the concurrency gate at the database layer,
// while the pool prevents one schedd from creating an unbounded goroutine
// storm when a large due batch is released at once.
func (d *Drain) dispatchParallel(ctx context.Context, rows []state.Invocation) {
	if len(rows) == 0 {
		return
	}
	workers := d.dispatchConcurrency
	if workers <= 1 {
		for _, inv := range rows {
			d.dispatchOne(ctx, inv)
		}
		return
	}
	if workers > len(rows) {
		workers = len(rows)
	}
	jobs := make(chan state.Invocation)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for inv := range jobs {
				d.dispatchOne(ctx, inv)
			}
		}()
	}
	for _, inv := range rows {
		jobs <- inv
	}
	close(jobs)
	wg.Wait()
}

// dispatchOne is per-row. The lifecycle:
//
//  1. Cap re-check (delayed_task only — config-drift protection).
//  2. ClaimInvocation (pending → dispatching, lease, attempts++).
//  3. engine.Wake (idempotent — may return an existing RUNNING instance).
//  4. StampInstanceInvocation — write the live handle onto the row
//     so the meter's CountInstanceInvocationsInMinute join lands.
//  5. gateway.Invoke (delivers envelope through wake gate).
//  6. CompleteInvocation (state → completed; result blob attached).
//
// Errors branch on transient vs permanent: transient = retryAfter 5s
// (Claim → re-set to pending); permanent = terminal failed. The
// gateway is responsible for surfacing ErrPermanentInvoke when the
// underlying HTTP status is 4xx; the engine's Wake surfaces
// ErrPermanentWake on no-such-app / no-live-deployment.
func (d *Drain) dispatchOne(ctx context.Context, inv state.Invocation) {
	// 1. Cap re-check (delayed_task source only — the plan may have
	// been downgraded between EnqueueInvocation and now).
	if inv.Source == state.InvocationDelayedTask {
		if d.isOverDelayedCap(ctx, inv.AppID) {
			// budget=0 keeps the legacy infinite-retry semantics on
			// the delayed-task-cap path; that path is not plan-scoped
			// (issue #394 only arms the budget for queue source).
			_ = d.store.FailInvocation(ctx, inv.ID, "delayed-task cap exceeded on dispatch", 30*time.Second, 0)
			d.log.Warn("drain: delayed-task cap on dispatch", "inv", inv.ID, "app_id", inv.AppID)
			return
		}
	}
	// 2. Account Active gate. The cron path has this (loop.go:580);
	// the drain needs it too because rows queued while the account was
	// Active may sit in 'pending' across a suspension (Free goes past
	// due → suspended). Cap+Activeness = the complete per-app gate.
	// 5-minute backoff is short enough that a reactivation lands within
	// a SLO; long enough that we don't churn cycles on a suspended
	// account.
	if !d.isAccountActive(ctx, inv.AppID) {
		// budget=0: account-suspended retry does not consume the
		// plan's per-message budget. Free customers hitting this path
		// (a Free app can't queue, but can be a delayed_task/cron
		// actor) never had a budget to begin with.
		_ = d.store.FailInvocation(ctx, inv.ID, "account suspended", 5*time.Minute, 0)
		d.log.Info("drain: account suspended; deferred",
			"inv", inv.ID, "app_id", inv.AppID)
		return
	}
	// 3. Claim.
	cap := d.accountAsyncCap(ctx, inv)
	if _, err := d.store.ClaimInvocationWithCap(ctx, inv.ID, "", d.wakeLeaseSeconds, cap); err != nil {
		if errors.Is(err, state.ErrQuotaExceeded) {
			// Per-account cap hit (ADR-134 PR-B). Leave the row
			// in 'pending' so the next tick retries after a
			// sibling completes. Counting it as a 'skip' here
			// keeps the drain from looping hot on a full cap.
			d.log.Info("drain: account async cap hit; deferring claim",
				"inv", inv.ID, "app_id", inv.AppID, "cap", cap)
			return
		}
		// Already-claimed (MemStore ErrNotFound, PgStore race): the
		// "skip locked" path caught us on the next LIST. Skip.
		return
	}

	// 3. EnsureWake coalesces same-app wake attempts while the bounded
	// dispatch pool lets different apps progress concurrently. Returns
	// the live instance handle on success; the drain stamps it onto the
	// row so the meter's per-instance count is non-zero for this minute.
	coord, err := d.engine.EnsureWake(ctx, inv.AppID, TriggerMeterd)
	if err == nil && coord.Err != nil {
		err = coord.Err
	}
	if err == nil && coord.Instance == nil {
		err = errors.New("sched: ensure wake returned no instance")
	}
	if err != nil {
		retryAfter := time.Duration(d.retryAfterSeconds) * time.Second
		// Permanent wake errors short-circuit to state='failed' — a
		// missing app, a deleted account, or a PARKED app will not
		// recover by waiting 5s. The engine
		// wraps the cause; we use errors.Is so the sentinels survive
		// any future fmt.Errorf wrapping.
		if errors.Is(err, ErrPermanentWake) {
			retryAfter = 0
		}
		_ = d.store.FailInvocation(ctx, inv.ID, "wake: "+err.Error(), retryAfter, d.queueAttemptBudget(ctx, inv), failOutcome(err))
		d.log.Warn("drain: wake", "inv", inv.ID, "err", err, "permanent", retryAfter == 0)
		return
	}
	wakeRes := WakeResult{
		InstanceID:   coord.Instance.InstanceID,
		NodeID:       coord.Instance.NodeID,
		DeploymentID: coord.Instance.DeploymentID,
		WakeID:       coord.Instance.WakeID,
		Port:         int(coord.Instance.Port),
	}
	// 4. Stamp the live instance handle. Failure here is non-fatal —
	// the dispatch can still proceed; the meter just under-counts
	// for this row. Logged so a regression in the stamp path is
	// visible without aborting the dispatch.
	if err := d.store.StampInstanceInvocation(ctx, inv.ID, wakeRes.InstanceID); err != nil {
		d.log.Warn("drain: stamp instance", "inv", inv.ID, "inst", wakeRes.InstanceID, "err", err)
	}
	// 5. Invoke (deliver envelope).
	if d.gateway == nil {
		// No gateway (test seam): the drain still completes the
		// row so the meter gets its tick.
		_ = d.store.CompleteInvocation(ctx, inv.ID, nil)
		d.emitDone(ctx, inv)
		return
	}
	var dispatched state.Invocation
	if prewoken, ok := d.gateway.(prewokenGatewaySynth); ok {
		dispatched, err = prewoken.InvokeWithWake(ctx, inv.AppID, inv, wakeRes)
	} else {
		dispatched, err = d.gateway.Invoke(ctx, inv.AppID, inv)
	}
	if err != nil {
		// Permanent invoke errors (4xx) terminal-fail; transient
		// (network / 5xx) retry. The gateway is the source of
		// truth — it knows whether the failure is recoverable.
		retryAfter := time.Duration(d.retryAfterSeconds) * time.Second
		if errors.Is(err, ErrPermanentInvoke) {
			retryAfter = 0
		}
		_ = d.store.FailInvocation(ctx, inv.ID, "invoke: "+err.Error(), retryAfter, d.queueAttemptBudget(ctx, inv), failOutcome(err))
		d.log.Warn("drain: invoke", "inv", inv.ID, "inst", wakeRes.InstanceID, "err", err, "permanent", retryAfter == 0)
		return
	}
	// 6. Complete.
	if err := d.store.CompleteInvocation(ctx, inv.ID, dispatched.Result); err != nil {
		// pgstore.ErrNotFound would mean someone else completed
		// first; drain does NOT have to retry — the row is in a
		// terminal state and the meter join will see it.
		d.log.Warn("drain: complete", "inv", inv.ID, "err", err)
		return
	}
	d.emitDone(ctx, inv)
}

// emitDone fires invocation_done so the dashboard SSE hook (a
// follow-up Move 2 PR) can light up. Today no listener subscribes;
// the channel is defined so the follow-up lands in one PR.
//
// The payload is built with json.Marshal rather than string
// concatenation to keep the CodeQL go/log-injection rules clean
// (the inputs are UUIDs so the bug is theoretical, but Marshal
// makes the audit story a no-op).
func (d *Drain) emitDone(ctx context.Context, inv state.Invocation) {
	if d.notifier == nil {
		return
	}
	body, err := json.Marshal(map[string]string{
		"invocation_id": inv.ID,
		"app_id":        inv.AppID,
		"source":        string(inv.Source),
		"state":         string(state.InvocationCompleted),
	})
	if err != nil {
		d.log.Warn("drain: marshal invocation_done", "inv", inv.ID, "err", err)
		return
	}
	if err := d.notifier.Notify(ctx, db.NotifyInvocationDone, string(body)); err != nil && !errors.Is(err, context.Canceled) {
		d.log.Warn("drain: notify invocation_done", "inv", inv.ID, "err", err)
	}
}

// isOverDelayedCap returns true when adding one more delayed_task to
// this app would push past the plan cap. Reads the cap dynamically
// (the customer may have downgraded) and delegates the count to
// CountPendingInvocations (index-backed by invocations_app_pending_idx).
func (d *Drain) isOverDelayedCap(ctx context.Context, appID string) bool {
	app, err := d.engine.Store().AppByID(ctx, appID)
	if err != nil {
		return false
	}
	acct, err := d.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		return false
	}
	limits := api.MustLimitsFor(acct.Plan)
	n, err := d.store.CountPendingInvocations(ctx, appID, state.InvocationDelayedTask)
	if err != nil {
		return false
	}
	return n >= limits.MaxDelayedTasksPerApp
}

// isAccountActive is the suspended-account gate for the drain. Mirrors
// the cron loop's `if !acct.Active() { continue }` at loop.go:580 —
// rows queued while the account was Active may sit in 'pending' across
// a suspension; the drain must not dispatch them.
//
// Caching: a 5s TTL on `account_id → Active` drops the per-row
// AccountByID round-trip to one per batch per account. The TTL is
// shorter than the suspended-skip's 5-minute backoff so a reactivation
// is honoured within the next tick (1s safety ticker).
//
// Fail-open: a transient lookup error returns `true` (Active). The
// drain's Cron sibling uses the same fail-open stance — never block the
// platform on a Postgres hiccup. The cron path's fail-open is the
// reason this is also fail-open: the cron loop's gate is the only
// reason the cron path is one less spot-check away from a "kill switch"
// regression.
func (d *Drain) isAccountActive(ctx context.Context, appID string) bool {
	app, err := d.engine.Store().AppByID(ctx, appID)
	if err != nil {
		return true
	}
	if cached, ok := d.accts.get(app.AccountID); ok {
		return cached.active
	}
	acct, err := d.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		return true
	}
	d.accts.put(app.AccountID, acct.Active(), d.now().Add(5*time.Second))
	return acct.Active()
}

// queueAttemptBudget (issue #394) returns the per-plan retry budget
// for an invocation, resolved from the parent account's plan. Returns
// 0 for non-queue sources (delayed_task, async_invoke, cron) and for
// any lookup error — Store.FailInvocation treats budget==0 as
// "infinite retry", which is the correct behaviour for those paths
// and a safe degrade for lookups.
//
// Plan caps rarely change (no churn from a healthy customer), so we
// don't cache the value here. The AccountByID round-trip is one extra
// store call per dispatched row; if this becomes hot, a TTL'd
// plan-cache keyed by account_id is the obvious follow-up.
//
// Telemetry: a lookup-error fail-open disables the retry ceiling for
// the affected row, which lets a poisoned payload retry indefinitely
// until the lookup recovers. The fail-open is the right behaviour for
// the in-flight row (don't dead-letter on a Postgres hiccup) but it
// MUST be observable — we slog a warning so the operator sees the
// gate fall back. Without this, a sustained Postgres blip would
// silently mask the dead-letter safety net issue #394 introduces.
func (d *Drain) queueAttemptBudget(ctx context.Context, inv state.Invocation) int {
	if inv.Source != state.InvocationQueue {
		return 0
	}
	app, err := d.engine.Store().AppByID(ctx, inv.AppID)
	if err != nil {
		d.log.WarnContext(ctx, "queueAttemptBudget: AppByID failed; falling back to legacy infinite retry",
			"inv_id", inv.ID, "app_id", inv.AppID, "err", err)
		return 0
	}
	acct, err := d.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		d.log.WarnContext(ctx, "queueAttemptBudget: AccountByID failed; falling back to legacy infinite retry",
			"inv_id", inv.ID, "app_id", inv.AppID, "account_id", app.AccountID, "err", err)
		return 0
	}
	return api.MustLimitsFor(acct.Plan).MaxQueueAttempts
}

// accountAsyncCap (ADR-134 PR-B) returns the per-plan cap on
// concurrent in-flight async invocations for the account owning
// inv. Resolved via the same AppByID → AccountByID → MustLimitsFor
// chain as queueAttemptBudget. Returns 0 on any lookup error so
// the drain refuses to claim (the safe degrade — see comment on
// queueAttemptBudget for the rationale).
func (d *Drain) accountAsyncCap(ctx context.Context, inv state.Invocation) int {
	app, err := d.engine.Store().AppByID(ctx, inv.AppID)
	if err != nil {
		d.log.WarnContext(ctx, "accountAsyncCap: AppByID failed; falling back to 0 cap",
			"inv_id", inv.ID, "app_id", inv.AppID, "err", err)
		return 0
	}
	acct, err := d.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		d.log.WarnContext(ctx, "accountAsyncCap: AccountByID failed; falling back to 0 cap",
			"inv_id", inv.ID, "app_id", inv.AppID, "account_id", app.AccountID, "err", err)
		return 0
	}
	return api.MustLimitsFor(acct.Plan).MaxAsyncInvocationsPerAccount
}
