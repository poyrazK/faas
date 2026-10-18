package alerts

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type mcpCatalogSignalKey struct{}

func (e *Evaluator) withMCPCatalogSignal(ctx context.Context, rule state.AlertRule) (context.Context, *api.MCPCatalogMonitorSignal) {
	var signal *api.MCPCatalogMonitorSignal
	if source, ok := e.store.(state.MCPCatalogMonitorStore); ok && rule.AppID != "" {
		readCtx, cancel := context.WithTimeout(ctx, api.MCPPolicyConvergenceReadTimeout)
		snapshot, err := source.GetMCPCatalogMonitor(readCtx, rule.AccountID, rule.AppID)
		cancel()
		if errors.Is(err, state.ErrNotFound) {
			signal = &api.MCPCatalogMonitorSignal{State: "disabled"}
		} else if err == nil {
			app, err := e.store.AppByID(ctx, rule.AppID)
			if err == nil && app.AccountID == rule.AccountID {
				observed := api.CatalogMonitorSummary(snapshot, app.Slug, e.now()).Signal
				signal = &observed
			}
		}
	}
	return context.WithValue(ctx, mcpCatalogSignalKey{}, signal), signal
}
func (e *Evaluator) observeMCPCatalogMonitor(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	if !api.AlertRuleActionAllowedForMetric(string(rule.Metric), string(rule.Action)) {
		return 0, false, skipDegraded
	}
	signal, _ := ctx.Value(mcpCatalogSignalKey{}).(*api.MCPCatalogMonitorSignal)
	if signal == nil {
		return 0, false, skipDegraded
	}
	if signal.State == "disabled" {
		return 0, false, skipPaused
	}
	var value float64
	if string(rule.Metric) == api.MetricMCPCatalogMonitorUnavailable {
		switch signal.State {
		case "unavailable":
			value = float64(len(signal.UnavailableRoles))
			if value == 0 {
				value = 1
			}
		case "healthy", "drift":
		default:
			return 0, false, skipDegraded
		}
	} else {
		switch signal.State {
		case "unavailable":
			if len(signal.DriftedRoles) == 0 {
				return 0, false, skipInsufficient
			}
			value = float64(signal.ChangedCount)
			// Fresh drift is a lower bound while another role is unknown.
			// It can prove a gt/gte breach, but cannot prove recovery.
			if value < 1 || (rule.Comparison != state.AlertGt && rule.Comparison != state.AlertGte) || !compareFloat(value, rule.Comparison, rule.Threshold) {
				return 0, false, skipInsufficient
			}
		case "drift":
			value = float64(signal.ChangedCount)
			if value < 1 {
				return 0, false, skipDegraded
			}
		case "healthy":
		default:
			return 0, false, skipDegraded
		}
	}
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}
