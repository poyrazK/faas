package alerts

import "github.com/onebox-faas/faas/pkg/state"

func isWorkflowMetric(metric state.AlertMetric) bool {
	switch metric {
	case state.AlertMetricWorkflowFailures, state.AlertMetricWorkflowQuotaSkips,
		state.AlertMetricWorkflowPendingAge, state.AlertMetricWorkflowWaitingAge, state.AlertMetricWorkflowDueAge:
		return true
	default:
		return false
	}
}
