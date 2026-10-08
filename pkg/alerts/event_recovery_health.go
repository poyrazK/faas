package alerts

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeEventRecovery(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.EventRecoveryHealthStore)
	if !ok {
		return 0, false, skipDegraded
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	health, err := store.GetEventRecoveryHealth(ctx, rule.AccountID, rule.AppID, e.now())
	if err != nil {
		return 0, false, skipDegraded
	}
	value := float64(health.StalledJobs)
	if string(rule.Metric) == "event_recovery_capacity_wait_jobs" {
		value = float64(health.ProlongedCapacityWaitJobs)
	}
	if string(rule.Metric) == "event_recovery_expiring_jobs" {
		value = float64(health.ExpiringJobs)
	}
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
