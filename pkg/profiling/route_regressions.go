package profiling

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateRegressionRoutes(routes []string) error {
	if len(routes) > api.ProfileRouteRegressionMaxRoutes {
		return errors.New("too many advisory routes: maximum 10")
	}
	seen := map[string]bool{}
	for _, route := range routes {
		if route == "" || route == api.ProfileUnattributedRoute || !api.ValidProfileRoute(route) || seen[route] {
			return errors.New("advisory routes require unique static method/path labels")
		}
		seen[route] = true
	}
	return nil
}

func unavailableRouteChecks(o api.ProfileRegressionOptions, reason string) []api.ProfileRouteRegression {
	checks := []api.ProfileRouteRegression{}
	if validateRegressionRoutes(o.Routes) != nil {
		return checks
	}
	for _, route := range o.Routes {
		checks = append(checks, api.ProfileRouteRegression{Route: route, Status: "insufficient_data", Reason: reason, CodeReason: "Code attribution unavailable: route comparison is insufficient."})
	}
	return checks
}

// AssessRouteRegressions never changes the parent regression status or rollout.
// Missing sampled routes are unknown; they are never substituted with zero CPU.
func AssessRouteRegressions(o api.ProfileRegressionOptions, a, b api.ProfileResponse) []api.ProfileRouteRegression {
	o = api.NormalizeProfileRegressionOptions(o)
	checks := unavailableRouteChecks(o, "Both windows need labeled route CPU and observed request telemetry.")
	if len(checks) == 0 || ValidateRegressionOptions(o) != nil {
		return checks
	}
	comparable := profileComparisonReason(a, b) == ""
	quality := CompareAttribution(a, b)
	for i := range checks {
		check := &checks[i]
		left, right := findRouteCPU(a, check.Route), findRouteCPU(b, check.Route)
		if left != nil {
			check.BaselineRequests = left.Requests
		}
		if right != nil {
			check.CandidateRequests = right.Requests
		}
		check.LabelCoverage = compareRouteLabels(a, b, check.Route, check.BaselineRequests, check.CandidateRequests)
		if !comparable || !sufficientCoverage(a, o) || !sufficientCoverage(b, o) {
			check.Reason = "Both windows need comparable profiles and sufficient capture coverage without recorded upload failures."
			continue
		}
		if !quality.Available || quality.SubstantialChange {
			check.Reason = "Attribution quality is unavailable or the labeled CPU share changed substantially; route CPU/request is inconclusive."
			continue
		}
		if left == nil || right == nil || left.Requests == nil || right.Requests == nil {
			continue
		}
		if *left.Requests < o.MinimumRequests || *right.Requests < o.MinimumRequests {
			check.Reason = "Both route windows need the configured minimum observed requests."
			continue
		}
		if !check.LabelCoverage.Consistent {
			check.Reason = check.LabelCoverage.Reason
			continue
		}
		if left.CPUSeconds <= 0 || right.CPUSeconds <= 0 {
			continue
		}
		metric, known := addRequestMetric(api.ProfileRegressionMetric{}, left.CPUSeconds/float64(*left.Requests), right.CPUSeconds/float64(*right.Requests), o)
		if !known || metric.CPUPerRequest.RelativeIncreasePercent == nil {
			continue
		}
		check.Metric = metric.CPUPerRequest
		check.Status = "no_regression_detected"
		check.Reason = "Observed route CPU/request did not meet both configured increase thresholds."
		if check.Metric.ExceedsThreshold {
			check.Status = "regressed"
			check.Reason = "Observed route CPU/request met both configured increase thresholds; this is advisory, not deployment causality."
		}
	}
	return checks
}

func findRouteCPU(p api.ProfileResponse, route string) *api.ProfileRouteCPU {
	for _, row := range p.Routes {
		if row.Route == route {
			return &row
		}
	}
	return nil
}
