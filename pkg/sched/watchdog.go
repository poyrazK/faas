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
	// WakingSweepBudget is the spec §6.1 budget for WAKING rows without a
	// usable snapshot. Snapshot-backed rows are exempted in runOne because the
	// restore path is allowed the cold-boot fallback budget.
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
		// A snapshot-backed wake can spend the restore budget in vmmd and then
		// fall back to a cold boot. Its engine context is bounded by
		// ColdBootTimeout, so the 5-second WAKING backstop must not race it.
		// Do not exempt the row forever, though: if the wake caller is
		// cancelled after vmmd has booted (or schedd is restarted), the row
		// can otherwise remain WAKING indefinitely and consume the app's
		// concurrency reservation. After the combined restore + cold-boot
		// budget, the normal WAKING kill path is the safe recovery.
		if st == state.StateWaking && ins.DeploymentID != "" {
			if _, snapErr := w.store.LatestSnapshot(ctx, ins.DeploymentID); snapErr == nil {
				if !ins.StartedAt.IsZero() && now.Sub(ins.StartedAt) < WakingSweepBudget+ColdBootSweepBudget {
					continue
				}
			}
		}
		// SNAPSHOTTING rows get a memory-scaled reprieve. The sweep
		// query uses one flat SnapshotSweepBudget (20s) so it stays a
		// single cheap index scan, but the engine gives each capture
		// SnapshotBudgetFor(ram) — 45s at Scale's 1024 MB. Without this
		// exemption the watchdog would kill a Scale snapshot at 20s
		// while it is legitimately still writing, turning a working
		// park into a FAILED instance and losing the snapshot.
		// Mirrors the WAKING exemption above.
		// Anchored on ParkedAt, not StartedAt: for a SNAPSHOTTING row
		// StartedAt is when the VM booted (possibly hours earlier),
		// which would make the reprieve expire instantly on any
		// long-lived instance. ParkedAt is when the capture began and
		// is the same anchor the sweep query ages on.
		if st == state.StateSnapshotting && !ins.ParkedAt.IsZero() {
			if now.Sub(ins.ParkedAt) < SnapshotBudgetFor(ins.RAMMB) {
				continue
			}
		}
		if err := w.engine.KillStuck(ctx, ins.ID, ins.AppID, reason); err != nil {
			w.log.Warn("watchdog: kill stuck", "instance", ins.ID, "state", st, "reason", reason, "err", err)
		}
	}
}
