package alerts

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// observeEnvironment evaluates a stage's alert rule over the stage's own
// signals (ADR-20261104120000 amendment 2026-11-22): request metrics the
// gateway counts per environment host, and the failed invocations the stage
// owns. A rule on any other signal is skipped; the store refuses enabling
// one.
func (e *Evaluator) observeEnvironment(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	if !state.StageAlertRuleRunnable(rule) {
		e.log.Warn("alerts: stage rule reads a signal stages do not have", "rule", rule.ID, "metric", rule.Metric)
		return 0, false, skipDegraded
	}
	if rule.Metric == state.AlertMetricFailedInvocs {
		return e.observeEnvironmentFailedInvocations(ctx, rule)
	}
	window := string(rule.WindowSpec)
	observed, source := appmetrics.FetchEnvironmentAlertMetric(ctx, e.promQL, e.log, rule.AppID, rule.EnvironmentID, window, string(rule.Metric))
	if source != appmetrics.SourcePrometheus {
		return 0, false, skipDegraded
	}
	breached := compareFloat(observed, rule.Comparison, rule.Threshold)
	if breached && ratioMetric(rule.Metric) {
		// As for production: a share of a handful of requests is not
		// evidence (ADR-20261109153000).
		requests, src := appmetrics.FetchEnvironmentAlertMetric(ctx, e.promQL, e.log, rule.AppID, rule.EnvironmentID, window, "request_count")
		if src != appmetrics.SourcePrometheus {
			return 0, false, skipDegraded
		}
		if requests < api.AlertRatioMinRequests {
			return observed, false, skipInsufficient
		}
	}
	return observed, breached, ""
}

func (e *Evaluator) observeEnvironmentFailedInvocations(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.AlertRuleEnvironmentStore)
	if !ok {
		return 0, false, skipDegraded
	}
	since := e.windowStart(rule.WindowSpec, e.now())
	sources := []state.InvocationSource{state.InvocationSource(rule.FailureSource)}
	if rule.FailureSource == "" || rule.FailureSource == state.AlertFailureAny {
		sources = []state.InvocationSource{state.InvocationAsyncInvoke, state.InvocationQueue, state.InvocationDelayedTask,
			state.InvocationCron, state.InvocationInboundWebhook}
	}
	total := 0
	for _, source := range sources {
		n, err := store.CountFailedInvocationsInEnvironmentSince(ctx, rule.AccountID, rule.AppID, rule.EnvironmentID, source, since)
		if err != nil {
			e.log.Warn("alerts: count stage failed invocations", "rule", rule.ID, "source", source, "err", err)
			return 0, false, skipDegraded
		}
		total += n
	}
	return float64(total), compareFloat(float64(total), rule.Comparison, rule.Threshold), ""
}
