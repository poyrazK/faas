package alerts

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeEventConsumerExecution(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.EventConsumerExecutionHealthStore)
	if !ok {
		return 0, false, skipDegraded
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	now := e.now()
	h, err := store.GetEventConsumerExecutionHealth(ctx, rule.AccountID, rule.AppID, rule.EventSubscriptionID, e.windowStart(rule.WindowSpec, now), now)
	if err != nil {
		return 0, false, skipDegraded
	}
	if h.Truncated || h.MissingRoots > 0 || h.Unknown > 0 || h.RetainedRoots == 0 {
		return 0, false, skipInsufficient
	}
	var value float64
	switch string(rule.Metric) {
	case "event_execution_dead_letters":
		value = float64(h.DeadLettered)
	case "event_execution_dead_letter_rate_per_second":
		if h.UnknownAttempts > 0 {
			return 0, false, skipInsufficient
		}
		value = h.DeadLetterRatePerSecond
	case "event_handler_failure_pct":
		if h.UnknownAttempts > 0 || h.SuccessfulAttempts+h.FailedAttempts < api.EventConsumerHealthMinFailureSamples {
			return 0, false, skipInsufficient
		}
		value = h.HandlerFailurePct
	case "event_completion_latency_p95_seconds":
		if h.WindowCompletions == 0 {
			return 0, false, skipInsufficient
		}
		value = h.CompletionLatencyP95Seconds
	default:
		return 0, false, skipDegraded
	}
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
