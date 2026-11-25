package sched

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

// UnplacedSweepInterval is how often schedd re-claims apps whose
// apps.node_id is still NULL.
const UnplacedSweepInterval = time.Minute

// SweepUnplaced claims an owner node for every non-deleted app whose
// apps.node_id is still NULL, and returns how many it found. It backs up
// the kind=created notify that PlacementClaimSubscriber acts on:
// pg_notify is fire-and-forget, and an app whose notify never arrived
// stays unplaced, so every gateway wake for it fails (prod 2026-11-25,
// project 8624ac80). ClaimUnplaced is idempotent, so peers sweeping the
// same rows are harmless. A failed claim is logged and the row is
// retried on the next sweep.
func (e *Engine) SweepUnplaced(ctx context.Context) (int, error) {
	apps, err := e.store.ListUnplacedApps(ctx)
	if err != nil {
		return 0, fmt.Errorf("sched: sweep unplaced: list: %w", err)
	}
	for _, a := range apps {
		if err := ctx.Err(); err != nil {
			return len(apps), err
		}
		if err := e.ClaimUnplaced(ctx, a.ID); err != nil {
			e.log.Warn("sched: sweep unplaced: claim", "app_id", a.ID, "err", err)
		}
	}
	return len(apps), nil
}

// RunUnplacedSweep sweeps once at start (a schedd that was down missed
// every kind=created sent meanwhile) and then every interval, jittered
// by ±10% so the schedds of a fleet do not query in lockstep. It logs
// only when a sweep found unplaced apps or failed, and returns when ctx
// ends.
func (e *Engine) RunUnplacedSweep(ctx context.Context, interval time.Duration) {
	for {
		n, err := e.SweepUnplaced(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			e.log.Warn("sched: sweep unplaced", "err", err)
		case n > 0:
			e.log.Info("sched: sweep unplaced: claimed unplaced apps", "count", n)
		}
		timer := time.NewTimer(jitterUnplacedSweep(interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// jitterUnplacedSweep spreads interval over [0.9, 1.1) × interval.
func jitterUnplacedSweep(interval time.Duration) time.Duration {
	if interval < 10 {
		return interval
	}
	//nolint:gosec // G404: sweep spacing jitter has no security value.
	return interval - interval/10 + rand.N(interval/5)
}
