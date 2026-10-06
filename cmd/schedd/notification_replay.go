package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/db"
)

// Snapshot prime returns after handing work to the scheduler pool. Its existing
// recovery reconciler owns that handoff, so the queued work retains the daemon
// lifetime rather than the completed delivery's context. Other replay handlers
// finish synchronously and use the renewable claim's cancellable context.
func durableReplayHandler(daemonCtx context.Context, handler func(context.Context, db.Notification) error) func(context.Context, db.Notification) error {
	return func(deliveryCtx context.Context, n db.Notification) error {
		if n.Channel == db.NotifySnapshotPrime {
			return handler(daemonCtx, n)
		}
		return handler(deliveryCtx, n)
	}
}
