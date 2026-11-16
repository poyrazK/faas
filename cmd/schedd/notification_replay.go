package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
)

// Snapshot prime returns after handing work to the scheduler pool. Its existing
// recovery reconciler owns that handoff, so the queued work retains the daemon
// lifetime rather than the completed delivery's context. Other replay handlers
// finish synchronously and use the renewable claim's cancellable context.
//
// peerDeferralDelay is the retry delay plus up to one poll interval of
// jitter. A fixed delay that is a whole number of poll intervals reopened the
// row just before the deferring peer's own next tick, so that peer claimed and
// deferred it again every time and the owner never did (production
// 2026-11-16: a credential rotation's restart sat pending for over 20 minutes,
// reopening every 6.000 s). Jitter moves the reopening across the poll period,
// so whichever schedd ticks first, the owner included, claims it next.
func peerDeferralDelay() time.Duration {
	return db.NotificationRetryDelay(1) + rand.N(db.NotificationOutboxPollInterval())
}

// Every schedd's replay worker claims any app_wake or runtime_config_restart
// row: the rows name no node. A schedd that does not own the app defers the
// row instead of failing it (ADR-191 amendment 2026-10-26, follow-up). A
// failure spent one of the row's bounded attempts and pushed the owner's next
// try out on the retry backoff, so peers could dead-letter a wake the owner
// never saw. A deferral gives the attempt back and keeps the peer's drain
// moving past the row.
func durableReplayHandler(daemonCtx context.Context, handler func(context.Context, db.Notification) error) func(context.Context, db.Notification) error {
	return func(deliveryCtx context.Context, n db.Notification) error {
		var err error
		if n.Channel == db.NotifySnapshotPrime {
			err = handler(daemonCtx, n)
		} else {
			err = handler(deliveryCtx, n)
		}
		if errors.Is(err, sched.ErrAppOwnedByAnotherNode) {
			return &db.DeferredNotificationError{Cause: err, Delay: peerDeferralDelay()}
		}
		return err
	}
}
