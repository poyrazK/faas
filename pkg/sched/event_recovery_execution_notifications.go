package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) runEventRecoveryExecutionNotifications(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.EventRecoveryExecutionNotificationStore)
	if !ok {
		return
	}
	for i := 0; i < eventFanoutRecoveryBatch; i++ {
		worked, err := store.ProcessNextEventRecoveryExecutionNotification(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: capture recovery execution notification failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}
