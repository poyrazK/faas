// watchdog.go is schedd's §6.1 state-transition watchdog (commit 3 of
// the lock-narrowing PR). Every second Loop.Run fires the Watchdog
// once; each tick does three state-bucket queries:
//   - WAKING rows older than 5s          → KillStuck(COLD_BOOTING fallback)
//   - COLD_BOOTING rows older than 30s   → KillStuck(FAILED)
//   - SNAPSHOTTING rows older than 20s   → KillStuck(STOPPED)
//
// The watchdog itself only logs+continues on a per-row KillStuck
// failure; one wedged row must never stall the rest of the sweep.

package sched

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// stateBudgets are the spec §6.1 deadlines, applied to the row's
// "age" — started_at for WAKING/COLD_BOOTING, parked_at for
// SNAPSHOTTING (parked_at is stamped on entry into SNAPSHOTTING in
// snapshotAndPark; see engine.go).
//
// These values mirror Wake's per-call vmmd deadlines from commit 1
// (WakingTimeout, ColdBootTimeout) within ±1s. The minor slack lets
// the per-call deadline fire first and transition the row to its
// terminal state cleanly; the watchdog is a backstop for cases where
// the per-call deadline doesn't (e.g. the vmmd call hangs but Wake
// has since returned nil — the only way the row stays in WAKING).
const (
	// WakingSweepBudget is the spec §6.1 budget for WAKING: rows
	// older than this when the watchdog ticks are killed.
	WakingSweepBudget = 5 * time.Second

	// ColdBootSweepBudget is the spec §6.1 budget for COLD_BOOTING.
	ColdBootSweepBudget = 30 * time.Second

	// SnapshotSweepBudget is the spec §6.1 budget for SNAPSHOTTING.
	// Matches park budget published in the spec.
	SnapshotSweepBudget = 20 * time.Second
)

// DefaultWatchdogInterval is the per-second cadence Loop.Run drives
// the Watchdog at. 1s is the spec-mandated fine grain (it's the same
// value that catches a hung Firecracker on a one-box with a single
// concurrent wake in flight).
const DefaultWatchdogInterval = 1 * time.Second

// retentionFirstFireDelay is the deliberate pause before Loop.Run's
// retention ticker fires for the first time (PR #74 review-fix). A
// bare time.NewTicker fires once immediately on construction; if
// schedd restarts and the sweep races the §6.1 watchdog's first
// tick, the backfill-anchored rows in migration 00017 (terminal_at
// = coalesce(parked_at, started_at, now())) would be deleted before
// the watchdog has had a chance to reclaim stuck rows from the
// previous run. One minute is long enough for the watchdog to
// establish state on a cold start, short enough that operators
// observe the first sweep inside a heartbeat window.
const retentionFirstFireDelay = 1 * time.Minute

// Watchdog owns one tick of the §6.1 sweep. It is stateless across
// ticks — each tick queries the store fresh — so a panicking tick
// does not corrupt subsequent ticks.
type Watchdog struct {
	store  state.Store
	engine *Engine
	log    *slog.Logger
	now    func() time.Time // injected for tests
}

// NewWatchdog wires the dependencies. store + engine must be non-nil.
// log may be nil (uses slog.Default).
func NewWatchdog(store state.Store, engine *Engine, log *slog.Logger) *Watchdog {
	if log == nil {
		log = slog.Default()
	}
	return &Watchdog{store: store, engine: engine, log: log, now: time.Now}
}

// WithClock replaces the tick-clock (tests). Returns the receiver for
// builder-style wiring.
func (w *Watchdog) WithClock(now func() time.Time) *Watchdog {
	w.now = now
	return w
}

// sweepRuns executes one watchdog tick. Public so tests can drive a
// tick without spinning up Loop.Run; Loop.Run calls this every
// DefaultWatchdogInterval seconds.
//
// Errors are logged per row and swallowed: one wedged row must never
// stall the rest of the sweep, and the watchdog has nothing to alert
// on that the failure log line wouldn't already cover.
func (w *Watchdog) sweepRuns(ctx context.Context) {
	now := w.now()

	w.runOne(ctx, now, state.StateWaking, WakingSweepBudget, StuckWakingTimeout)
	w.runOne(ctx, now, state.StateColdBooting, ColdBootSweepBudget, StuckColdBootTimeout)
	w.runOne(ctx, now, state.StateSnapshotting, SnapshotSweepBudget, StuckSnapshotTimeout)
}

// runOne is one bucket of the sweep.
func (w *Watchdog) runOne(ctx context.Context, now time.Time, st state.State, budget time.Duration, reason StuckReason) {
	threshold := now.Add(-budget)
	rows, err := w.store.ListInstancesByStatesOlderThan(ctx, []state.State{st}, threshold)
	if err != nil {
		w.log.Warn("watchdog: lookup", "state", st, "err", err)
		return
	}
	for _, ins := range rows {
		if err := w.engine.KillStuck(ctx, ins.ID, ins.AppID, reason); err != nil {
			w.log.Warn("watchdog: kill stuck", "instance", ins.ID, "state", st, "reason", reason, "err", err)
		}
	}
}
