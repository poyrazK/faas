package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"math"
	"strings"
	"testing"
)

func TestRetainedRouteCodeEvidenceValidation(t *testing.T) {
	percent := 100.0
	e := api.ProfileRegressionEvidence{Kind: "function", Frames: []api.ProfileCallPathFrame{{Name: "checkout", File: "app.js", BaselinePath: "app.js", CandidatePath: "app.js"}}, Metric: api.ProfileRegressionMetric{BaselineCPUPerSecond: 1, CandidateCPUPerSecond: 2, DeltaCPUPerSecond: 1, ExceedsThreshold: true, CPUPerRequest: &api.ProfileRegressionCPUPerRequestMetric{BaselineCPUSecondsPerRequest: .1, CandidateCPUSecondsPerRequest: .2, DeltaCPUSecondsPerRequest: .1, RelativeIncreasePercent: &percent, ExceedsThreshold: true}}}
	check := api.ProfileRouteRegression{Status: "regressed", CodeEvidence: []api.ProfileRegressionEvidence{e}}
	if err := validateRouteCodeEvidence(check); err != nil {
		t.Fatal(err)
	}
	check.Status = "insufficient_data"
	if validateRouteCodeEvidence(check) == nil {
		t.Fatal("accepted code from insufficient route data")
	}
	check.Status = "regressed"
	check.CodeEvidence[0].Frames[0].BaselineSource = &api.ProfileSourceLocation{URL: "https://example.com"}
	if validateRouteCodeEvidence(check) == nil {
		t.Fatal("retained source URL")
	}
	check.CodeEvidence[0].Frames[0].BaselineSource = nil
	check.CodeEvidence[0].Frames[0].BaselinePath = "../secret"
	if validateRouteCodeEvidence(check) == nil {
		t.Fatal("accepted traversal")
	}
	check.CodeEvidence[0].Frames[0].BaselinePath = "app.js"
	check.CodeEvidence[0].Metric.CPUPerRequest.DeltaCPUSecondsPerRequest = math.NaN()
	if validateRouteCodeEvidence(check) == nil {
		t.Fatal("accepted NaN")
	}
	check.CodeEvidence[0].Metric.CPUPerRequest.DeltaCPUSecondsPerRequest = .1
	check.CodeReason = strings.Repeat("x", 1025)
	if validateRouteCodeEvidence(check) == nil {
		t.Fatal("accepted oversized explanation")
	}
}
