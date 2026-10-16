package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) runWorkflowRecoveryHealthAlerts(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowRecoveryHealthAlertStore)
	if !ok {
		return
	}
	for i := 0; i < api.WorkflowRecoveryPolicyPollBatch; i++ {
		worked, err := store.ProcessNextWorkflowRecoveryHealthAlert(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: recovery health alert failed", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}
