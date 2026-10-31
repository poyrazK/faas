package routehealth

import "github.com/onebox-faas/faas/pkg/api"

// Empty action is the backwards-compatible hold policy for existing callers.
func RegressionAction(action string) string {
	if action == "" {
		return "hold"
	}
	return action
}

// AbortEligible requires confirmed error regression on a selected route. An
// absolute latency budget alone cannot establish that restoring stable helps.
// Evidence is always evaluated freshly by APID; saved decisions are not tokens.
func AbortEligible(report api.RouteHealthReport) bool {
	if report.Mode != "enforce" || report.OnRegression != "abort" || report.Status != "regressed" || report.StableDeploymentID == "" {
		return false
	}
	for _, route := range report.Routes {
		finding := route
		SummarizeFinding(&finding)
		if finding.ErrorStatus == "regressed" {
			return true
		}
	}
	return false
}

func AbortDecision(report api.RouteHealthReport) api.RouteHealthDecision {
	decision := Decision(report)
	decision.Status, decision.Reason = "aborted", "consecutive_server_error_regression"
	return decision
}

// WithSavedAbort reports the evidence the guard aborted the canary on when
// newest, the deployment's newest saved decision, is that abort. An aborted
// candidate serves no traffic, so a fresh evaluation of the current windows
// reads unknown (comparisons_incomplete_or_unsettled) although the guard
// aborted it on a confirmed regression. Customer impact stays live.
func WithSavedAbort(live api.RouteHealthReport, newest *api.RouteHealthHistoryEntry) api.RouteHealthReport {
	if newest == nil || newest.Purpose != "abort" || newest.Decision.Status != "aborted" ||
		newest.Report.DeploymentID != live.DeploymentID || newest.Report.AppID != live.AppID {
		return live
	}
	saved := newest.Report
	saved.Customers = live.Customers
	return saved
}
