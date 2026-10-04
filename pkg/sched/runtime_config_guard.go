package sched

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// runtimeConfigStale reports whether the app's secrets or environment changed
// after ins's runtime configuration was resolved (issue #3360). Guest-init
// reads the environment once at boot, so such a process still holds the
// previous values. Capturing it would publish a fresh snapshot of the old
// environment after apid already invalidated every existing one, and the next
// wake would restore it: a rotated credential would never reach the app
// without a redeploy, and a deleted one would come back.
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
	return ok && !runtimeConfigResolvedAt(ins).After(changedAt)
}

// runtimeConfigResolvedAt is the latest time ins's environment and sealed
// secrets can have been resolved. started_at is stamped when the instance
// reaches running, but the boot resolves its environment earlier, when the
// wake is admitted. Production (prod hunt #3): a secret deleted during a
// 10 s cold boot was still delivered, the instance's started_at landed after
// the delete, the restart path's init capture passed this guard, and every
// later restore brought the deleted secret back. The wake id is a UUIDv7
// minted at admission, before resolution, so the earlier of the two never
// postdates the configuration the process holds.
func runtimeConfigResolvedAt(ins state.Instance) time.Time {
	resolved := ins.StartedAt
	if id, err := uuid.Parse(ins.WakeID); err == nil && id.Version() == 7 {
		sec, nsec := id.Time().UnixTime()
		if admitted := time.Unix(sec, nsec); admitted.Before(resolved) {
			resolved = admitted
		}
	}
	return resolved
}
