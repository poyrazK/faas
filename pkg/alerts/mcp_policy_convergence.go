package alerts

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type mcpConvergenceKey struct{}
type mcpConvergenceObservation struct {
	Snapshot *api.MCPPolicyConvergenceSnapshot
}

func withMCPPolicyConvergenceSnapshot(ctx context.Context, store Store, rule state.AlertRule) (context.Context, *api.MCPPolicyConvergenceSnapshot) {
	var snapshot *api.MCPPolicyConvergenceSnapshot
	if source, ok := store.(state.MCPPolicyConvergenceStore); ok && rule.AppID != "" {
		readCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
		result, err := source.MCPPolicyConvergenceSnapshot(readCtx, rule.AccountID, rule.AppID)
		cancel()
		if err == nil {
			snapshot = &result
		}
	}
	return context.WithValue(ctx, mcpConvergenceKey{}, mcpConvergenceObservation{snapshot}), snapshot
}
func (e *Evaluator) observeMCPPolicyConvergence(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	if !api.AlertRuleActionAllowedForMetric(string(rule.Metric), string(rule.Action)) {
		return 0, false, skipDegraded
	}
	observation, _ := ctx.Value(mcpConvergenceKey{}).(mcpConvergenceObservation)
	snapshot := observation.Snapshot
	if snapshot == nil || (snapshot.State != "active" && snapshot.State != "pending" && snapshot.State != "unverified" && snapshot.State != "paused") {
		return 0, false, skipDegraded
	}
	if snapshot.State == "paused" {
		return 0, false, skipPaused
	}
	var value float64
	switch rule.Metric {
	case state.AlertMetricMCPPolicyUnverified:
		if snapshot.State == "unverified" {
			value = 1
		}
	case state.AlertMetricMCPPolicyUnconvergedGateways:
		if snapshot.State == "unverified" {
			return 0, false, skipInsufficient
		}
		if snapshot.State != "active" && snapshot.State != "pending" {
			return 0, false, skipDegraded
		}
		value = float64(snapshot.UnconvergedGateways)
	}
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
