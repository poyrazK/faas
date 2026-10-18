package alerts

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type readinessMonitorKey struct{}

func (e *Evaluator) withMCPReadiness(ctx context.Context, rule state.AlertRule) (context.Context, []api.MCPReadinessMonitorStatus) {
	var statuses []api.MCPReadinessMonitorStatus
	if api.ValidateMCPReadinessAlertScope(string(rule.Metric), rule.MCPReadinessScope) != nil {
		return context.WithValue(ctx, readinessMonitorKey{}, statuses), statuses
	}
	if source, ok := e.store.(state.MCPReadinessMonitorStore); ok && rule.AppID != "" {
		read, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
		var scopes []string
		if rule.MCPReadinessScope != "" {
			scopes = []string{rule.MCPReadinessScope}
		}
		result, err := source.ListMCPReadinessMonitoring(read, rule.AccountID, rule.AppID, "", scopes...)
		cancel()
		if err == nil && result != nil {
			statuses = make([]api.MCPReadinessMonitorStatus, 0, len(result))
			for _, status := range result {
				status.Scope = api.MCPConnectionScope(status.Scope)
				if rule.MCPReadinessScope != "" && status.Scope != rule.MCPReadinessScope {
					continue
				}
				statuses = append(statuses, status)
			}
		}
	}
	return context.WithValue(ctx, readinessMonitorKey{}, statuses), statuses
}
func (e *Evaluator) observeMCPReadiness(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	if !api.AlertRuleActionAllowedForMetric(string(rule.Metric), string(rule.Action)) {
		return 0, false, skipDegraded
	}
	statuses, _ := ctx.Value(readinessMonitorKey{}).([]api.MCPReadinessMonitorStatus)
	if statuses == nil {
		return 0, false, skipDegraded
	}
	if len(statuses) == 0 {
		return 0, false, skipPaused
	}
	count := 0
	pending := false
	for _, s := range statuses {
		switch api.ReadinessMonitorState(s, e.now()) {
		case "firing", "stale":
			count++
		case "pending":
			pending = true
		}
	}
	value := float64(count)
	if pending && (count == 0 || (rule.Comparison != state.AlertGt && rule.Comparison != state.AlertGte) || !compareFloat(value, rule.Comparison, rule.Threshold)) {
		return 0, false, skipInsufficient
	}
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
