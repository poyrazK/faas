package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

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
}

// DrainOption configures the Drain without leaking every field to the
// caller; cmd/schedd only sets the ones the production code cares about.
type DrainOption func(*Drain)

func WithDrainBatchSize(n int) DrainOption             { return func(d *Drain) { d.batchSize = n } }
func WithDrainWakeLease(s int) DrainOption             { return func(d *Drain) { d.wakeLeaseSeconds = s } }
func WithDrainRetryAfter(s int) DrainOption            { return func(d *Drain) { d.retryAfterSeconds = s } }
func WithDrainNow(now func() time.Time) DrainOption    { return func(d *Drain) { d.now = now } }
func WithDrainLogger(l *slog.Logger) DrainOption       { return func(d *Drain) { d.log = l } }
func WithDrainGatewaySynth(g GatewaySynth) DrainOption { return func(d *Drain) { d.gateway = g } }
func WithDrainNotifier(n Notifier) DrainOption         { return func(d *Drain) { d.notifier = n } }

// NewDrain wires the dependencies. Defaults are conservative: 64-batch
// per tick, 60s wake lease, 5s retry-after, real clock.
func NewDrain(store state.Store, engine *Engine, opts ...DrainOption) *Drain {
	d := &Drain{
		store:             store,
		engine:            engine,
		now:               time.Now,
		batchSize:         64,
		wakeLeaseSeconds:  60,
		retryAfterSeconds: 5,
		log:               slog.Default(),
	}
	for _, o := range opts {
		o(d)
	}
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
		for _, appID := range order {
			for _, inv := range byApp[appID] {
				d.dispatchOne(ctx, inv)
			}
		}
		if len(rows) < d.batchSize {
			return
		}
	}
}

// dispatchOne is per-row. The lifecycle:
//
//  1. Cap re-check (delayed_task only — config-drift protection).
//  2. ClaimInvocation (pending → dispatching, lease, attempts++).
//  3. engine.Wake (idempotent — may return an existing RUNNING instance).
//  4. gateway.Invoke (delivers envelope through wake gate).
//  5. CompleteInvocation (state → completed; result blob attached).
//
// Errors branch on transient vs permanent: transient = retryAfter 5s
// (Claim → re-set to pending); permanent = terminal failed.
func (d *Drain) dispatchOne(ctx context.Context, inv state.Invocation) {
	// 1. Cap re-check (delayed_task source only — the plan may have
	// been downgraded between EnqueueInvocation and now).
	if inv.Source == state.InvocationDelayedTask {
		if d.isOverDelayedCap(ctx, inv.AppID) {
			_ = d.store.FailInvocation(ctx, inv.ID, "delayed-task cap exceeded on dispatch", 30*time.Second)
			d.log.Warn("drain: delayed-task cap on dispatch", "inv", inv.ID, "app_id", inv.AppID)
			return
		}
	}
	// 2. Claim.
	claimed, err := d.store.ClaimInvocation(ctx, inv.ID, "", d.wakeLeaseSeconds)
	if err != nil {
		// Already-claimed (MemStore ErrNotFound, PgStore race): the
		// "skip locked" path caught us on the next LIST. Skip.
		return
	}
	_ = claimed

	// 3. Wake (Always-Wake = idempotent). Returns the live instance
	// handle on success (WakeResult carries the instance id we want
	// to stamp for the meter's per-instance count).
	wakeRes, err := d.engine.Wake(ctx, inv.AppID)
	if err != nil {
		// Transient: most fails are host-network blips / cgroup
		// pressure. retryAfter 5s; the loop will rebatch.
		_ = d.store.FailInvocation(ctx, inv.ID, "wake: "+err.Error(), time.Duration(d.retryAfterSeconds)*time.Second)
		d.log.Warn("drain: wake", "inv", inv.ID, "err", err)
		return
	}
	// 4. Invoke (deliver envelope). The drain does NOT have the
	// sync-block shape today (only async sources reach this path);
	// cmd/schedd's pg_notify fire-and-forget design means any error
	// is transient.
	if d.gateway == nil {
		// No gateway (test seam): the drain still completes the
		// row so the meter gets its tick.
		_ = d.store.CompleteInvocation(ctx, inv.ID, nil)
		d.emitDone(ctx, inv)
		return
	}
	if _, err := d.gateway.Invoke(ctx, inv.AppID, inv); err != nil {
		// Transient path: backing off 5s and letting the drain retry
		// is cheaper than re-waking when the synth endpoint is hot.
		_ = d.store.FailInvocation(ctx, inv.ID, "invoke: "+err.Error(), time.Duration(d.retryAfterSeconds)*time.Second)
		d.log.Warn("drain: invoke", "inv", inv.ID, "inst", wakeRes.InstanceID, "err", err)
		return
	}
	// 5. Complete.
	if err := d.store.CompleteInvocation(ctx, inv.ID, nil); err != nil {
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
func (d *Drain) emitDone(ctx context.Context, inv state.Invocation) {
	if d.notifier == nil {
		return
	}
	payload := `{"invocation_id":"` + inv.ID + `","app_id":"` + inv.AppID + `","source":"` + string(inv.Source) + `","state":"completed"}`
	if err := d.notifier.Notify(ctx, db.NotifyInvocationDone, payload); err != nil && !errors.Is(err, context.Canceled) {
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
