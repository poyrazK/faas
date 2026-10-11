package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep a live HTTP operation's claim while its wake/handler is in progress.
// Process death stops renewal; normal lease recovery then records uncertainty.
func (d *Drain) operationClaimContext(ctx context.Context, inv state.Invocation) (context.Context, func()) {
	store, ok := d.store.(state.OperationExecutionLeaseStore)
	if !state.InvocationHasOperation(inv) || !ok || inv.DeadlineAt == nil {
		return ctx, func() {}
	}
	lease := min(d.wakeLeaseSeconds, int(api.OperationExecutionLeaseMax/time.Second))
	if lease <= 0 {
		return ctx, func() {}
	}
	claimCtx, cancel := context.WithDeadline(ctx, *inv.DeadlineAt)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Duration(lease) * time.Second / 3)
		defer ticker.Stop()
		for {
			select {
			case <-claimCtx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(claimCtx, api.OperationExecutionRenewTimeout)
				renewed, err := store.RenewOperationExecution(renewCtx, inv.ID, inv.Attempts, lease)
				stop()
				if err != nil || !renewed {
					d.log.Warn("drain: operation claim renewal stopped", "inv", inv.ID, "attempt", inv.Attempts, "err", err)
					cancel()
					return
				}
			}
		}
	}()
	return claimCtx, func() { cancel(); <-done }
}
