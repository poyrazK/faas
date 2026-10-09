package profiling

import (
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestRouteCodeEvidenceIsolationAndRequests(t *testing.T) {
	count := int64(100)
	check := api.ProfileRouteRegression{Route: "POST /checkout", Status: "regressed", Metric: &api.ProfileRegressionCPUPerRequestMetric{}, BaselineRequests: &count, CandidateRequests: &count}
	a, b := regressionProfile(100, 20), regressionProfile(100, 40)
	a.Query.Route, b.Query.Route = check.Route, check.Route
	o := api.DefaultProfileRegressionOptions()
	out := RouteCodeEvidence(check, o, a, b)
	if len(out.CodeEvidence) != 2 {
		t.Fatalf("missing route function/path evidence: %+v", out)
	}
	for _, e := range out.CodeEvidence {
		if e.Metric.CPUPerRequest == nil || e.Metric.CPUPerRequest.DeltaCPUSecondsPerRequest < .1999 {
			t.Fatal(e)
		}
	}
	// A hot profile from any other route (including aggregate profiles) is unusable.
	for _, route := range []string{"GET /health", ""} {
		b.Query.Route = route
		out = RouteCodeEvidence(check, o, a, b)
		if len(out.CodeEvidence) != 0 || !strings.Contains(out.CodeReason, "unavailable") {
			t.Fatal(out)
		}
	}
	b.Query.Route = check.Route
	// Twice the requests explains twice the CPU; no code increase is reported.
	twice := int64(200)
	check.CandidateRequests = &twice
	out = RouteCodeEvidence(check, o, a, b)
	if len(out.CodeEvidence) != 0 {
		t.Fatal(out)
	}
	check.CandidateRequests = &count
	b.Empty = true
	out = RouteCodeEvidence(check, o, a, b)
	if len(out.CodeEvidence) != 0 || !strings.Contains(out.CodeReason, "unavailable") {
		t.Fatal(out)
	}
	b.Empty = false
	check.Status = "insufficient_data"
	out = RouteCodeEvidence(check, o, a, b)
	if len(out.CodeEvidence) != 0 {
		t.Fatal(out)
	}
}
