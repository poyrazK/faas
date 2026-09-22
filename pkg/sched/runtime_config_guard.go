package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeConfigStale reports whether the app's secrets or environment changed
// after ins's process started (issue #3360). Guest-init reads the environment
// once at boot, so such a process still holds the previous values. Capturing
// it would publish a fresh snapshot of the old environment after apid already
// invalidated every existing one, and the next wake would restore it: a
// rotated credential would never reach the app without a redeploy.
//
// A failed read reports stale. Snapshots are a cache (ADR-005): discarding a
// capture costs one cold boot, while publishing a stale one silently keeps a
// credential the customer asked to replace.
func (e *Engine) runtimeConfigStale(ctx context.Context, ins state.Instance) bool {
	changedAt, ok, err := e.store.AppRuntimeConfigChangedAt(ctx, ins.AppID)
	if err != nil {
		e.log.Warn("sched: runtime config change lookup failed; not snapshotting",
			"instance", ins.ID, "app", ins.AppID, "err", err)
		return true
	}
	return ok && !ins.StartedAt.IsZero() && changedAt.After(ins.StartedAt)
}
