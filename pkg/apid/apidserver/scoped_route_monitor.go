package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) scopedRouteMonitoring(ctx context.Context, acct state.Account, app state.App, env state.ProjectEnvironment, scope string) api.AppOperationalMonitoring {
	out := api.AppOperationalMonitoring{Status: "unknown", Reason: "environment_monitor_unavailable", Coverage: "deployment_attributed_current_windows"}
	store, ok := s.store.(state.ScopedRouteMonitorStore)
	if !ok {
		return out
	}
	if !acct.Plan.DebugTelemetryEnabled() {
		out.Reason = "plan_unavailable"
		return out
	}
	var budgets api.ScopedRouteBudgetReport
	var err error
	if scoped, ok := s.store.(state.ScopedRouteBudgetReportStore); ok {
		budgets, err = scoped.ReadScopedRouteBudgetReport(ctx, acct.ID, app.ID, scope)
		if budgets.Scope != scope || budgets.ProjectID != env.ProjectID || budgets.EnvironmentID != env.ID {
			return out
		}
	} else {
		budgets.Report, err = store.ReadScopedRouteMonitor(ctx, acct.ID, app.ID, scope)
		budgets.Scope, budgets.ProjectID, budgets.EnvironmentID, budgets.BudgetSource = scope, env.ProjectID, env.ID, "app_policy"
	}
	r := budgets.Report
	if err != nil || r.AppID != app.ID {
		return out
	}
	out.Available, out.Status, out.Reason, out.DeploymentID = true, r.Status, r.Reason, r.DeploymentID
	out.CheckedAt = &r.CheckedAt
	out.RouteBudgets = &budgets
	appOperationalWindows(&out, r)
	return out
}
func scopedMonitorRecommendations(m api.AppOperationalMonitoring) []api.AppOperationalRecommendation {
	if m.Available && m.Status == "healthy" {
		return []api.AppOperationalRecommendation{}
	}
	if m.Available && m.Status == "violated" {
		return []api.AppOperationalRecommendation{{Code: "environment_budget_violated", Severity: "warning", Message: "Selected environment exceeded a route budget in consecutive completed observation windows.", Next: "Inspect scoped route budgets and the exact serving deployment before choosing a recovery action."}}
	}
	return []api.AppOperationalRecommendation{{Code: "environment_budget_unknown", Severity: "info", Message: "Selected environment route budgets are disabled, unavailable or lack sufficient settled observations.", Next: "Inspect monitoring reason and scoped route windows; unknown is not evidence of health."}}
}
