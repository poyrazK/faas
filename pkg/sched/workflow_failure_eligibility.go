package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *Loop) observeWorkflowRecoveryFailures(ctx context.Context, now time.Time) {
	store, ok := l.engine.store.(state.WorkflowFailureEligibilityStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	for i := 0; i < api.WorkflowRecoveryFailureObserverBatch; i++ {
		worked, err := store.ObserveNextWorkflowFailureEligibility(ctx, now)
		if err != nil {
			if l.log != nil {
				l.log.Warn("sched: observe workflow failure eligibility", "err", err)
			}
			return
		}
		if !worked {
			return
		}
	}
}
