package promotion

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion/healthdispatch"
	"github.com/onebox-faas/faas/pkg/state"
)

// RunHealthWorker keeps claims short and never holds a lease in a local queue.
// Store claims enforce both fleet and account concurrency.
func RunHealthWorker(ctx context.Context, store state.Store, actions HealthActions, report func(*state.PromotionHealthJob, error)) {
	scheduler, ok := store.(state.ProjectPromotionHealthScheduler)
	if !ok {
		return
	}
	if _, ok = store.(state.ProjectPromotionHealthStore); !ok {
		return
	}
	healthdispatch.Run(ctx, healthdispatch.Options{Workers: api.ProjectPromotionHealthConcurrency, PollInterval: api.ProjectPromotionHealthPollInterval, ClaimTimeout: api.ProjectPromotionHealthPollInterval, JobTimeout: api.ProjectPromotionHealthJobTimeout}, scheduler.ClaimNextProjectEnvironmentPromotionHealth, func(ctx context.Context, job state.PromotionHealthJob, token string) error {
		return ReconcileClaimedHealth(ctx, store, job, actions, token)
	}, report)
}
