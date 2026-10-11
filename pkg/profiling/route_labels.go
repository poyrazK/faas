package profiling

import (
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func routeLabelCoverage(p api.ProfileResponse, route string, observed *int64) *api.ProfileRouteLabelCoverage {
	out := &api.ProfileRouteLabelCoverage{ObservedRequests: observed, Reason: "Labeled-request reports are missing or incomplete for the capture window."}
	c := p.Coverage
	if c == nil {
		return out
	}
	out.CapturedProfiles, out.BoundaryProfiles = c.LabelCountProfiles, c.LabelCountBoundaryProfiles
	if !c.Available || !c.LabelCountsComplete || c.LabelCountProfiles == 0 || c.LabelCounts == nil {
		return out
	}
	count := c.LabelCounts[routeRequestHash(route)]
	out.LabeledRequests = &count
	if observed == nil || *observed <= 0 {
		out.Reason = "Observed route request telemetry is unavailable or empty."
		return out
	}
	if count > *observed {
		out.Reason = "Application-reported labeled requests exceed observed traffic; counts cannot be reconciled."
		return out
	}
	percent := 100 * float64(count) / float64(*observed)
	out.Percent = &percent
	out.Available = true
	out.Reason = "Application-reported labeled entries in retained captures divided by observed requests in the whole window. Uncaptured intervals and boundary captures are not estimated."
	return out
}

func AttachRouteLabelCoverage(p *api.ProfileResponse) {
	for i := range p.Routes {
		row := &p.Routes[i]
		if row.Route != api.ProfileUnattributedRoute {
			row.LabelCoverage = routeLabelCoverage(*p, row.Route, row.Requests)
		}
	}
}

func compareRouteLabels(a, b api.ProfileResponse, route string, left, right *int64) *api.ProfileRouteLabelComparison {
	out := &api.ProfileRouteLabelComparison{Baseline: routeLabelCoverage(a, route, left), Candidate: routeLabelCoverage(b, route, right), MinimumPercent: api.ProfileRouteMinimumLabelCoveragePercent, MaximumChangePercentagePoints: api.ProfileRouteLabelMaxChangePercentagePoints, Reason: "Both windows need reconciled labeled-request counts and observed traffic."}
	if !out.Baseline.Available || !out.Candidate.Available {
		return out
	}
	out.Available = true
	delta := *out.Candidate.Percent - *out.Baseline.Percent
	out.DeltaPercentagePoints = &delta
	if *out.Baseline.Percent < out.MinimumPercent || *out.Candidate.Percent < out.MinimumPercent || math.Abs(delta) >= out.MaximumChangePercentagePoints {
		out.Reason = "Route labeling coverage is below the minimum or differs substantially between windows."
		return out
	}
	out.Consistent = true
	out.Reason = "Observed route labeling shares meet the minimum and consistency thresholds; application reports do not prove complete instrumentation."
	return out
}
