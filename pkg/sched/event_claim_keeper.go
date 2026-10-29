package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// keepEventClaims calls renew every state.PublishedEventLeaseRenewal until
// the returned stop is called (ADR-20261028131500): a slow sweep keeps the
// claims it still holds, and a crashed one loses them within
// state.PublishedEventLease. Stop waits for a renewal in flight.
func (l *Loop) keepEventClaims(ctx context.Context, renew func(context.Context, time.Time) error) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	every := l.eventClaimRenewalEvery
	if every <= 0 {
		every = state.PublishedEventLeaseRenewal
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := renew(ctx, l.eventFanoutNow()); err != nil && ctx.Err() == nil && l.log != nil {
					l.log.Warn("sched: renew event claims failed", "error_class", dispatchErrorClass(err))
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
