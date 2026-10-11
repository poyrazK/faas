package profiling

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RouteCodeEvidence consumes only profiles filtered to the selected route.
// Aggregate route coverage must be qualified before calling this function.
func RouteCodeEvidence(check api.ProfileRouteRegression, options api.ProfileRegressionOptions, a, b api.ProfileResponse) api.ProfileRouteRegression {
	check.CodeEvidence = nil
	check.CodeReason = "Code attribution unavailable: both windows need qualified samples for this route."
	if check.Status == "insufficient_data" || check.Metric == nil || a.Query.Route != check.Route || b.Query.Route != check.Route || a.Empty || b.Empty {
		return check
	}
	options = api.NormalizeProfileRegressionOptions(options)
	options.Metric, options.Routes = "cpu_per_request", nil
	out := NewRegressionAssessment(api.ProfileInvestigation{Investigation: api.ProfileInvestigationInput{Baseline: a.Query, Candidate: b.Query}}, options, time.Now())
	out = AssessRegressionWithRequests(out, a, b, check.BaselineRequests, check.CandidateRequests)
	used := 0
	for _, e := range out.Evidence {
		body, err := json.Marshal(e)
		if err != nil || used+len(body) > api.ProfileRouteCodeMaxEvidenceBytes {
			continue
		}
		used += len(body)
		check.CodeEvidence = append(check.CodeEvidence, e)
	}
	if out.Total == nil {
		check.CodeReason = "Code attribution unavailable: " + out.Reason
		return check
	}
	check.CodeReason = "Route-filtered function self CPU and inclusive call paths, ranked by CPU/request increase. Overlapping paths must not be added together; observations do not establish causality."
	if len(out.Evidence) == 0 {
		check.CodeReason = "No comparable route function or call path met both increase thresholds. Missing or zero-baseline symbols remain unknown."
	}
	if len(out.Evidence) > len(check.CodeEvidence) {
		check.CodeReason += " Some code evidence exceeded the retained route budget."
	}
	return check
}
