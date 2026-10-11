package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runWorkflowReadinessSubscriptionWorker(ctx context.Context) {
	worker, ok := s.store.(state.OperationWorkflowReadinessSubscriptionWorkerStore)
	if !ok {
		return
	}
	tick := time.NewTicker(api.OperationWorkflowReadinessSubscriptionPollInterval)
	defer tick.Stop()
	for {
		if _, err := worker.EvaluateDueWorkflowReadinessSubscriptions(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("workflow readiness subscription evaluation will retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
