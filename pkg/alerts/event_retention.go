package alerts

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeEventRetention(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	window, err := api.EventConsumerHealthWindow(string(rule.WindowSpec))
	if err != nil {
		return 0, false, skipDegraded
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRetentionRequestTimeout)
	defer cancel()
	if string(rule.Metric) == "event_storage_utilization_pct" {
		return e.observeEventStorageUtilization(ctx, rule)
	}
	store, ok := e.store.(state.EventRetentionStore)
	if !ok {
		return 0, false, skipDegraded
	}
	health, err := store.GetEventRetentionHealth(ctx, rule.AccountID, rule.AppID, api.EventRetentionQuery{Window: window, Limit: 1}, e.now())
	if err != nil {
		return 0, false, skipDegraded
	}
	if string(rule.Metric) == "event_retention_expiring_receipts" && health.UnknownDeadlineReceipts > 0 {
		return 0, false, skipDegraded
	}
	value := float64(health.ExpiringReceipts + health.EligibleForPruning)
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}

func (e *Evaluator) observeEventStorageUtilization(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.EventStorageUsageStore)
	if !ok {
		return 0, false, skipDegraded
	}
	usage, err := store.EventStorageUsage(ctx, rule.AccountID)
	if err != nil || usage.Limits.RetainedEvents <= 0 || usage.Limits.RetainedBytes <= 0 {
		return 0, false, skipDegraded
	}
	value := max(100*float64(usage.RetainedEvents)/float64(usage.Limits.RetainedEvents), 100*float64(usage.RetainedBytes)/float64(usage.Limits.RetainedBytes))
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
