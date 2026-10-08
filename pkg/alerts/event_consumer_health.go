package alerts

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeEventConsumer(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	if api.IsEventConsumerExecutionAlertMetric(string(rule.Metric)) {
		return e.observeEventConsumerExecution(ctx, rule)
	}
	store, ok := e.store.(state.EventConsumerHealthStore)
	if !ok {
		return 0, false, skipDegraded
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventSubscriptionControlTimeout)
	defer cancel()
	now := e.now()
	h, err := store.GetEventConsumerHealth(ctx, rule.AccountID, rule.AppID, rule.EventSubscriptionID, e.windowStart(rule.WindowSpec, now), now)
	if err != nil {
		return 0, false, skipDegraded
	}
	metric := string(rule.Metric)
	if h.Paused && metric != "event_paused_seconds" {
		return 0, false, skipPaused
	}
	var value float64
	switch metric {
	case "event_pending_recipients":
		value = float64(h.PendingRecipients)
	case "event_oldest_pending_seconds":
		value = h.OldestAgeSeconds
	case "event_paused_seconds":
		value = h.PausedSeconds
	default:
		if h.HistoryCompacted {
			return 0, false, skipInsufficient
		}
		switch metric {
		case "event_retry_rate_per_second":
			value = h.RetryRatePerSecond
		case "event_terminal_failure_pct":
			if h.SuccessfulRoutes+h.TerminalFailures < api.EventConsumerHealthMinFailureSamples {
				return 0, false, skipInsufficient
			}
			value = h.TerminalFailurePct
		case "event_routing_latency_p95_seconds":
			if h.SuccessfulRoutes == 0 {
				return 0, false, skipInsufficient
			}
			value = h.RoutingLatencyP95Seconds
		case "event_drain_rate_per_second":
			if h.PendingRecipients+h.ProcessingRecipients == 0 {
				return 0, false, skipInsufficient
			}
			value = h.DrainRatePerSecond
		default:
			return 0, false, skipDegraded
		}
	}
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
