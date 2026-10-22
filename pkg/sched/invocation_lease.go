package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// renewsInvocationLease reports whether this drain keeps a long invocation's
// claim by renewal (ADR-1180) rather than claiming it for the whole run.
// Operation invocations keep their own renewal (operationClaimContext).
func (d *Drain) renewsInvocationLease(inv state.Invocation) bool {
	_, ok := d.store.(state.InvocationLeaseStore)
	return ok && d.wakeLeaseSeconds > 0 && !state.InvocationHasOperation(inv)
}

// keepInvocationLease renews a long invocation's claim every third of its
// lease while it is delivered (ADR-1180). A scheduler that stops renewing,
// because it crashed or was restarted, lets the claim lapse within one lease,
// and the expired-lease sweep requeues the invocation; its interrupted
// delivery was cancelled with its connection. The keeper tolerates one failed
// renewal. When the row is no longer this attempt's, or the lease would
// lapse, it cancels the delivery so a requeued attempt cannot overlap it.
func (d *Drain) keepInvocationLease(ctx context.Context, inv state.Invocation) (context.Context, func()) {
	store, ok := d.store.(state.InvocationLeaseStore)
	if !ok || !d.renewsInvocationLease(inv) {
		return ctx, func() {}
	}
	lease := time.Duration(d.wakeLeaseSeconds) * time.Second
	interval := lease / 3
	keepCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		renewedAt := time.Now()
		for {
			select {
			case <-keepCtx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(keepCtx, api.OperationExecutionRenewTimeout)
				renewed, err := store.RenewInvocationLease(renewCtx, inv.ID, inv.Attempts, d.wakeLeaseSeconds)
				stop()
				switch {
				case err == nil && renewed:
					renewedAt = time.Now()
				case err == nil || time.Since(renewedAt)+interval >= lease:
					d.log.Warn("drain: invocation lease renewal stopped; cancelling delivery",
						"inv", inv.ID, "attempt", inv.Attempts, "err", err)
					cancel()
					return
				default:
					d.log.Warn("drain: invocation lease renewal failed; retrying",
						"inv", inv.ID, "attempt", inv.Attempts, "err", err)
				}
			}
		}
	}()
	return keepCtx, func() { cancel(); <-done }
}
