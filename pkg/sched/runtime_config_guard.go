package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeConfigStale verifies the inputs acknowledged for this process, rather
// than inferring them from readiness time. Guest-init reads the environment
// once at boot, so such a process still holds the previous values. Capturing
// it would publish a fresh snapshot of the old environment after apid already
// invalidated every existing one, and the next wake would restore it: a
// rotated credential would never reach the app without a redeploy.
//
// A failed read reports stale. Snapshots are a cache (ADR-005): discarding a
// capture costs one cold boot, while publishing a stale one silently keeps a
// credential the customer asked to replace.
func (e *Engine) runtimeConfigStale(ctx context.Context, ins state.Instance) bool {
	deployment, err := e.store.DeploymentByID(ctx, ins.DeploymentID)
	if err != nil || deployment.AppID != ins.AppID {
		return true
	}
	changedAt, ok, err := state.RuntimeConfigChangedAtForScope(ctx, e.store, ins.AppID, deployment.Scope)
	if err != nil {
		e.log.Warn("sched: runtime config change lookup failed; not snapshotting",
			"instance", ins.ID, "app", ins.AppID, "err", err)
		return true
	}
	if !ok {
		changedAt = time.Unix(0, 0).UTC()
	}
	return e.runtimeConfigReceiptStale(ctx, ins, changedAt, deployment.Scope)
}
