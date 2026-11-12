package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/events"
)

// noteWakeServedByInstance records, on the requested wake's own timeline,
// which instance answered it when that instance kept another wake_id: a
// thawed frozen VM or one already running. Without it the requested wake's
// timeline ended at app.wake_requested and looked lost (#6639). A wake that
// stamped its id on a new instance needs no link.
func (e *Engine) noteWakeServedByInstance(ctx context.Context, appID, requestedWakeID string, served *CoordInstance) {
	if e.events == nil || served == nil || requestedWakeID == "" || served.WakeID == requestedWakeID {
		return
	}
	e.events.EmitAsync(ctx, events.ServedByInstance{
		EmitAt: time.Now().UTC(), WakeID: requestedWakeID, AppID: appID,
		InstanceID: served.InstanceID, InstanceWakeID: served.WakeID, NodeID: served.NodeID,
	})
}
