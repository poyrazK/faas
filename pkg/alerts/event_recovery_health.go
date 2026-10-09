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
	if api.IsEventRecoveryExecutionAlertMetric(string(rule.Metric)) {
		if health.Execution == nil {
			return 0, false, skipDegraded
		}
		execution := health.Execution
		var value float64
		switch rule.Metric {
		case state.AlertMetricEventRecoveryExecutionWaitingJobs:
			value = float64(execution.WaitingJobs)
		case state.AlertMetricEventRecoveryExecutionProlongedWaitJobs:
			value = float64(execution.ProlongedWaitJobs)
		case state.AlertMetricEventRecoveryExecutionUnknownJobs:
			value = float64(execution.UnknownJobs)
		case state.AlertMetricEventRecoveryExecutionRetentionRiskJobs:
			value = float64(execution.RetentionRiskJobs)
		}
		exceeds := compareFloat(value, rule.Comparison, rule.Threshold)
		if !execution.CountsComplete && (!exceeds || (rule.Comparison != state.AlertGt && rule.Comparison != state.AlertGte)) {
			return 0, false, skipDegraded
		}
		return value, exceeds, ""
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
