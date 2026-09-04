// snapshot_backoff.go — capped-exponential backoff for the
// snapshot-cache-miss hot loop (Workstream B / issue #1184 /
// ADR-137).
//
// Why a wake-side backoff and not just a retry: a deployment
// whose snapshot row is missing (the FC upgrade race, the
// imaged-side write race, the GC reaper evicted the row before
// the next wake landed) hits the snapshot-miss path on every
// wake request. Each miss costs a cold-boot (~3-5s wall, ~RAM
// admission reservation). Under sustained misses (deployment
// misconfigured, image registry unreachable, FC upgrade in
// flight), the wake hot loop burns RAM + capacity indefinitely
// without making progress.
//
// The backoff stamps per-deployment snapshot_miss_count +
// snapshot_miss_backoff_until (migration 00585). The wake
// path consults DeploymentSnapshotBackoffActive BEFORE the
// snapshot lookup; if backoff is active, the wake returns
// 503 with Retry-After = ceil(backoff - now) so the customer's
// next request lands inside the cooldown window. Capped
// exponential: 5s base × 2^n, 300s max, 6 attempts before the
// window freezes at max. After 6 misses the backoff holds at
// 300s until DeploymentClearSnapshotBackoff is called (a
// successful snapshot_written event resets the counter).
//
// This file is the wake-side facade: pure backoff math + the
// store hook. The retry-After HTTP header is surfaced by the
// gateway error mapping (cmd/gatewayd-internal); the
// schedd-side primitive just stamps the row and returns the
// retry-after seconds so the gateway can echo them.
package sched

import (
	"context"
	"math"
	"time"
)

// snapshotBackoffBase is the first miss's cooldown (5s). The
// 5s base matches the §6.1 WAKING timeout window — a wake
// that hits the backoff at T=0 lands a retry inside the same
// WAKING-budget window the gatewayd already reserves.
const snapshotBackoffBase = 5 * time.Second

// snapshotBackoffMax caps the cooldown at 300s (5 minutes).
// Past 5 minutes the customer-facing latency is already worse
// than the cold-boot path (which converges in ~30s), so the
// cap prevents indefinite backoff from being worse than the
// alternative.
const snapshotBackoffMax = 300 * time.Second

// snapshotBackoffMaxAttempts freezes the counter at this
// value; further misses stamp backoff_until = max without
// raising the count. The threshold mirrors §12's
// snapshot_fleet_avg_mb alert (160 MB sustained for 5m) so
// the two tripwires move together.
const snapshotBackoffMaxAttempts = 6

// ComputeSnapshotBackoff returns the (backoff duration, retry-
// after seconds) pair for the (n+1)-th consecutive miss.
// Pure function — same input, same output — so the unit test
// pins the closed set without a store fixture.
//
//	n    duration    retry_after_seconds
//	0    5s          5
//	1    10s         10
//	2    20s         20
//	3    40s         40
//	4    80s         80
//	5    160s        160
//	6+   300s        300   (frozen at max)
func ComputeSnapshotBackoff(consecutiveMisses int) (time.Duration, int) {
	if consecutiveMisses < 0 {
		consecutiveMisses = 0
	}
	if consecutiveMisses >= snapshotBackoffMaxAttempts {
		return snapshotBackoffMax, int(snapshotBackoffMax / time.Second)
	}
	// 5s × 2^n. math.Pow gives a float64 we cast back; the
	// rounding error at large n is irrelevant — the value is
	// then clamped to snapshotBackoffMax below.
	d := time.Duration(math.Pow(2, float64(consecutiveMisses))) * snapshotBackoffBase
	if d > snapshotBackoffMax {
		d = snapshotBackoffMax
	}
	retryAfter := int(math.Ceil(d.Seconds()))
	return d, retryAfter
}

// snapshotBackoffRetryAfter returns the remaining whole seconds for an
// already-recorded cooldown. The wire contract never emits zero, even when a
// row expires between the database read and response construction.
func snapshotBackoffRetryAfter(until *time.Time) int {
	if until == nil {
		return 1
	}
	seconds := int(math.Ceil(time.Until(until.UTC()).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// RecordSnapshotMiss stamps the per-deployment backoff row and
// returns the new backoff duration + retry-after seconds for
// the caller to surface to the gateway. The store stamp is
// best-effort: a transient PG blip logs Warn but the wake
// proceeds (the next miss will stamp correctly, and the in-
// flight wake's cold-boot path is still safe).
func (e *Engine) RecordSnapshotMiss(ctx context.Context, deploymentID string, currentMisses int) (time.Duration, int, error) {
	if e == nil || e.store == nil {
		return 0, 0, nil
	}
	d, retryAfter := ComputeSnapshotBackoff(currentMisses)
	if err := e.store.DeploymentRecordSnapshotMiss(ctx, deploymentID, time.Now().UTC().Add(d)); err != nil {
		if e.log != nil {
			e.log.Warn("sched: snapshot backoff: stamp failed",
				"deployment_id", deploymentID, "err", err)
		}
		return d, retryAfter, nil
	}
	if e.ops != nil {
		e.ops.SnapshotBackoffStamp("recorded").Inc()
	}
	return d, retryAfter, nil
}

// ClearSnapshotBackoff resets the per-deployment counter on a
// successful snapshot_written event (cmd/schedd subscribes to
// imaged's notify channel). Idempotent — a missing row is a
// benign no-op.
func (e *Engine) ClearSnapshotBackoff(ctx context.Context, deploymentID string) error {
	if e == nil || e.store == nil {
		return nil
	}
	if err := e.store.DeploymentClearSnapshotBackoff(ctx, deploymentID); err != nil {
		if e.log != nil {
			e.log.Warn("sched: snapshot backoff: clear failed",
				"deployment_id", deploymentID, "err", err)
		}
		return err
	}
	if e.ops != nil {
		e.ops.SnapshotBackoffStamp("cleared").Inc()
	}
	return nil
}
