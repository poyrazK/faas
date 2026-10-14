package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// freezeFromReaper tries to freeze an instance the idle reaper selected
// (ADR-1151). It reports true when the instance was frozen, or must stay
// running this tick because a request raced the freeze; false means the
// reaper parks it as before.
func (l *Loop) freezeFromReaper(ctx context.Context, instanceID string, residentMB int) bool {
	freezeCtx, cancel := context.WithTimeout(ctx, reaperParkTimeout)
	defer cancel()
	handled, err := l.engine.FreezeIdle(freezeCtx, instanceID, residentMB)
	if err != nil {
		l.log.Warn("reaper: freeze", "instance", instanceID, "err", err)
		return false
	}
	return handled
}

// parkExpiredFrozen parks frozen instances that outlived FrozenTTL and
// publishes how many stay frozen (ADR-1151).
func (l *Loop) parkExpiredFrozen(ctx context.Context, snapshot []InstanceInfo, now time.Time) {
	frozen := 0
	for _, in := range snapshot {
		if in.State != state.StateFrozen {
			continue
		}
		timeout := time.Duration(EffectiveIdleTimeoutS(in.Plan, in.AppType, in.IdleTimeoutS)) * time.Second
		expired := frozenExpired(now, in, timeout)
		if !expired && !in.DeploymentSuperseded && !in.DeploymentEnded {
			frozen++
			continue
		}
		reason := "ttl"
		if !expired {
			reason = "superseded"
		}
		parkCtx, cancel := context.WithTimeout(ctx, reaperParkTimeout)
		err := l.engine.ParkFrozen(parkCtx, in.Instance, reason)
		cancel()
		if err != nil {
			l.log.Warn("reaper: park frozen instance", "instance", in.Instance, "reason", reason, "err", err)
			frozen++
		}
	}
	l.ops.SetFrozenInstances(frozen)
}
