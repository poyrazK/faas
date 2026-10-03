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
