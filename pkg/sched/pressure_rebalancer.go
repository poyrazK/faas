// pkg/sched/pressure_rebalancer.go — Tier A9 (ADR-087)
// capacity-pressure-triggered cross-node rebalance watcher.
//
// Polls Engine.pressureAggregator every
// api.PressureReassessmentIntervalSeconds (default 30s); for each
// app that's crossed the threshold, increments the per-app
// sweep counter and invokes the caller-supplied handle with the
// app id. The interesting policy (peer selection, cooldown,
// conditional UPDATE, metric, pressure_rebalanced notify) lives
// in Engine.RebalancePressuredApps (engine.go); this watcher
// keeps to the "filter + dispatch" loop pattern shared with
// pkg/sched/rebalancer.go + pkg/sched/router_watcher.go.
//
// Architectural note: the trigger is in-process (the
// aggregator), not pg_notify-driven. AtCapacity is a per-call
// engine outcome, not a row-write event — a pg_notify consumer
// would need to write every AtCapacity event to a table first,
// doubling the write path. The in-process aggregator is
// strictly cheaper and race-free (single schedd-internal mutex).
//
// Failure modes:
//   - handle returns err: log Warn, continue.
//   - ctx cancel: return.
//   - no pressured apps: no-op (the metric is the closed-set
//     dashboard-panel; the loop runs at the heartbeat cadence
//     regardless of activity).

package sched

import (
	"context"
	"time"
)

// PressureRebalancerHandle is the per-app work function the
// watcher invokes. The cold-start sweep in cmd/schedd calls
// Engine.RebalancePressuredApps directly with
// appID="" (the engine's per-app method handles "" as a
// no-op; the watcher enumerates via the aggregator). A nil
// return is success; non-nil is logged-and-continued by the
// watcher.
type PressureRebalancerHandle func(ctx context.Context, appID string) error

// PressureRebalancerLogger is the minimal slog surface this
// watcher needs. Mirrors RebalancerLogger. Tests pass nil and
// the watcher logs nothing.
type PressureRebalancerLogger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// PressureRebalancer polls the aggregator on a fixed cadence
// and dispatches pressured apps. Failures log Warn and never
// propagate; the loop is expected to outlive transient blips.
type PressureRebalancer struct {
	agg               *PressureAggregator
	thresholdPerMin   int
	reassessmentEvery time.Duration
	beforeSweepHook   func(appID string) // bumps the engine's per-app sweep counter
	handle            PressureRebalancerHandle
	log               PressureRebalancerLogger
}

// NewPressureRebalancer wires the watcher with the aggregator
// + cadence + handle. handle MUST be non-nil; the watcher
// panics on a nil handle so a missed wiring surfaces at startup
// rather than as silent rebalance dead-air on the first sweep.
//
// beforeSweepHook is optional (nil tolerated). Production wires
// it to Engine.IncrementPressureSweepCounter so the policy
// gate (migrate_after_2) reads the current sweep count when
// the handle is invoked.
func NewPressureRebalancer(agg *PressureAggregator, thresholdPerMin int, reassessment time.Duration, beforeSweepHook func(appID string), handle PressureRebalancerHandle, log PressureRebalancerLogger) *PressureRebalancer {
	if agg == nil {
		panic("sched: NewPressureRebalancer: aggregator is nil")
	}
	if thresholdPerMin <= 0 {
		panic("sched: NewPressureRebalancer: thresholdPerMin must be > 0")
	}
	if reassessment <= 0 {
		panic("sched: NewPressureRebalancer: reassessment must be > 0")
	}
	if handle == nil {
		panic("sched: NewPressureRebalancer: handle is nil (pressure rebalancer will dead-air at first sweep)")
	}
	return &PressureRebalancer{
		agg:               agg,
		thresholdPerMin:   thresholdPerMin,
		reassessmentEvery: reassessment,
		beforeSweepHook:   beforeSweepHook,
		handle:            handle,
		log:               log,
	}
}

// Run drives the ticker loop until ctx is cancelled. Returns
// ctx.Err() on cancellation. Each tick queries the aggregator
// for PressuredApps and dispatches the handle per app in
// sorted order (the aggregator's deterministic order).
func (r *PressureRebalancer) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.reassessmentEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick is the per-cadence work unit. Filter to pressured apps
// via the aggregator; dispatch to handle. Each step logs on
// failure but never propagates — the loop must outlive a
// transient per-app failure so a single bad app doesn't
// starve the rest.
//
// Splits out of Run so the test surface can drive a manual
// tick without the time.Ticker (the test seeds the
// aggregator, calls tick once, asserts the dispatch).
//
// Clock seam: reads r.agg.Now() (the aggregator's clock —
// time.Now in production, frozenClock in tests). Reading
// wall-clock time.Now here would silently bypass the test
// seam: a test that seeds events at frozen `now` and runs for
// >window would see every event GC'd because PressuredApps's
// cutoff is anchored to wall-clock time rather than the
// test's frozen anchor.
func (r *PressureRebalancer) tick(ctx context.Context) {
	apps := r.agg.PressuredApps(r.thresholdPerMin, r.agg.Now())
	if len(apps) == 0 {
		return
	}
	for _, appID := range apps {
		if r.beforeSweepHook != nil {
			r.beforeSweepHook(appID)
		}
		if err := r.handle(ctx, appID); err != nil {
			if r.log != nil {
				r.log.Warn("sched: pressure rebalancer: handle failed",
					"app_id", appID, "err", err)
			}
		}
	}
}

// RunColdStartSweep is the once-per-startup hook the schedd
// main wires after the engine is ready. Reads the aggregator
// for any app above the threshold (i.e. a schedd that was
// down while sustained pressure built up) and dispatches the
// handle per app. Returns the number of apps swept.
//
// Clock seam: reads r.agg.Now() — same reasoning as tick;
// the aggregator's clock is the single anchor for windowed
// reads across the package.
func (r *PressureRebalancer) RunColdStartSweep(ctx context.Context) int {
	apps := r.agg.PressuredApps(r.thresholdPerMin, r.agg.Now())
	for _, appID := range apps {
		if r.beforeSweepHook != nil {
			r.beforeSweepHook(appID)
		}
		if err := r.handle(ctx, appID); err != nil {
			if r.log != nil {
				r.log.Warn("sched: pressure rebalancer: cold-start sweep failed",
					"app_id", appID, "err", err)
			}
		}
	}
	return len(apps)
}
