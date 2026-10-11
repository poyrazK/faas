package main

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// All additional queries share the parent timeout and admission slots. Never
// substitute aggregate hotspots when route-filtered evidence is unavailable.
func (s *server) attachRouteCodeEvidence(ctx context.Context, acct state.Account, app state.App, out *api.ProfileRegressionAssessment) {
	for i := range out.RouteChecks {
		check := &out.RouteChecks[i]
		check.CodeReason = "Code attribution unavailable: route coverage or labeled samples are insufficient."
		if check.Status == "insufficient_data" {
			continue
		}
		a, b := out.Baseline, out.Candidate
		a.Route, b.Route = check.Route, check.Route
		left, problem := s.queryAppProfile(ctx, acct, app, a)
		if problem != nil {
			check.CodeReason = "Code attribution unavailable: baseline route profile could not be read."
			continue
		}
		right, problem := s.queryAppProfile(ctx, acct, app, b)
		if problem != nil {
			check.CodeReason = "Code attribution unavailable: candidate route profile could not be read."
			continue
		}
		*check = profiling.RouteCodeEvidence(*check, out.Options, left, right)
	}
	trimRouteCodeEvidence(out)
}

func trimRouteCodeEvidence(out *api.ProfileRegressionAssessment) {
	for i := len(out.RouteChecks) - 1; i >= 0; i-- {
		for len(out.RouteChecks[i].CodeEvidence) > 0 {
			body, err := json.Marshal(out)
			if err == nil && len(body) <= api.ProfileRegressionMaxAssessmentBytes {
				return
			}
			evidence := out.RouteChecks[i].CodeEvidence
			out.RouteChecks[i].CodeEvidence = evidence[:len(evidence)-1]
			out.RouteChecks[i].CodeReason = "Route code evidence was truncated to fit the retained assessment budget."
		}
	}
	// A nearly full aggregate summary may leave no room for optional route text.
	// Preserve the aggregate evidence; the dashboard provides an unavailable fallback.
	for i := len(out.RouteChecks) - 1; i >= 0; i-- {
		body, err := json.Marshal(out)
		if err == nil && len(body) <= api.ProfileRegressionMaxAssessmentBytes {
			return
		}
		out.RouteChecks[i].CodeReason = ""
	}
}

// Source URLs are derived from owned deployment provenance at read time.
func (s *server) enrichRouteCodeSources(ctx context.Context, app state.App, a, b api.ProfileQuery, checks []api.ProfileRouteRegression) []api.ProfileRouteRegression {
	out := append([]api.ProfileRouteRegression(nil), checks...)
	evidence := []api.ProfileRegressionEvidence{}
	for i := range out {
		out[i].CodeEvidence = append([]api.ProfileRegressionEvidence(nil), out[i].CodeEvidence...)
		for j := range out[i].CodeEvidence {
			out[i].CodeEvidence[j].Frames = append([]api.ProfileCallPathFrame(nil), out[i].CodeEvidence[j].Frames...)
			evidence = append(evidence, out[i].CodeEvidence[j])
		}
	}
	if len(evidence) == 0 {
		return out
	}
	signal := api.CanaryProfileSignal{Baseline: &a, Candidate: &b, Evidence: evidence}
	s.enrichCanaryProfileSignal(ctx, app, &signal, nil)
	return out
}
