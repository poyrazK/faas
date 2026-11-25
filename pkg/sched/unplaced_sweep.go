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
// apps.node_id is still NULL. It returns how many it found and how many
// of those it could not claim. It backs up
// the kind=created notify that PlacementClaimSubscriber acts on:
// pg_notify is fire-and-forget, and an app whose notify never arrived
// stays unplaced, so every gateway wake for it fails (prod 2026-11-25,
// project 8624ac80). ClaimUnplaced is idempotent, so peers sweeping the
// same rows are harmless. Failed claims stay NULL for the next sweep and
// are logged as one line per sweep, not one per app: an app that fits no
// node would otherwise log on every schedd every minute.
func (e *Engine) SweepUnplaced(ctx context.Context) (found, failed int, err error) {
	apps, err := e.store.ListUnplacedApps(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("sched: sweep unplaced: list: %w", err)
	}
	var firstAppID string
	var firstErr error
	for _, a := range apps {
		if err := ctx.Err(); err != nil {
			return len(apps), failed, err
		}
		if err := e.ClaimUnplaced(ctx, a.ID); err != nil {
			if failed == 0 {
				firstAppID, firstErr = a.ID, err
			}
			failed++
		}
	}
	if failed > 0 {
		e.log.Warn("sched: sweep unplaced: claims failed",
			"failed", failed, "found", len(apps), "first_app_id", firstAppID, "first_err", firstErr)
	}
	return len(apps), failed, nil
}

// RunUnplacedSweep sweeps once at start (a schedd that was down missed
// every kind=created sent meanwhile) and then every interval, jittered
// by ±10% so the schedds of a fleet do not query in lockstep. It logs
// only when a sweep claimed apps or failed, and returns when ctx ends.
func (e *Engine) RunUnplacedSweep(ctx context.Context, interval time.Duration) {
	for {
		found, failed, err := e.SweepUnplaced(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			e.log.Warn("sched: sweep unplaced", "err", err)
		case found > failed:
			e.log.Info("sched: sweep unplaced: claimed unplaced apps", "count", found-failed)
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
