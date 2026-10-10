package debugger

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSynthesizePrioritizesGuestFailure(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{
			Status: 502,
			Guest:  &api.DebugGuestExecutionEvidence{Outcome: "timeout", ErrorClass: "timeout"},
		},
		Correlation: api.DebugRequestCorrelation{Stages: []api.DebugRequestCorrelationStage{
			{Phase: "guest", Status: "observed", DurationMS: 1000},
		}},
	}

	got := Synthesize(evidence)
	if got.Diagnosis != "request_failure" || got.Confidence != "high" {
		t.Fatalf("diagnosis = %q/%q, want request_failure/high", got.Diagnosis, got.Confidence)
	}
	if len(got.Findings) == 0 || got.Findings[0].Code != "request_failure" {
		t.Fatalf("findings = %+v, want request_failure first", got.Findings)
	}
}

// adr: 934 — a regressed dependency is named in the headline.
func TestSynthesizeNamesRegressedDependency(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request:    api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "GET /checkout", P95MS: 240, P95BaseMS: 120, Factor: "2.0"},
		DependencyComparison: &api.DebugDependencyDeploymentComparison{
			CurrentDeploymentID: "dep-81", PreviousDeploymentID: "dep-80", PreviousDeploymentTag: "v80",
			Dependencies: []api.DebugDependencyLatencyItem{
				{Type: "application", Name: "render", CurrentP95MS: 30, BaselineP95MS: 29},
				{Type: AppDependencyType, Kind: "postgresql", Name: "SELECT orders", BaselineP95MS: 82, CurrentP95MS: 191, RegressionFactor: 2.33, Regression: true},
			},
		},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "dependency_regression" || got.Confidence != "high" {
		t.Fatalf("diagnosis = %q/%q, want dependency_regression/high", got.Diagnosis, got.Confidence)
	}
	want := `postgresql "SELECT orders" slowed from 82ms to 191ms p95 since the previous deployment (v80).`
	if got.Headline != want {
		t.Fatalf("headline = %q, want %q", got.Headline, want)
	}
	found := false
	for _, finding := range got.Findings {
		found = found || finding.Code == "dependency_regression"
	}
	if !found {
		t.Fatalf("findings = %+v, want dependency_regression", got.Findings)
	}
}

func TestSynthesizeIgnoresUnregressedComparison(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		DependencyComparison: &api.DebugDependencyDeploymentComparison{
			Dependencies: []api.DebugDependencyLatencyItem{{Type: AppDependencyType, Kind: "redis", Name: "GET", BaselineP95MS: 2, CurrentP95MS: 3}},
		},
	}
	if got := Synthesize(evidence); got.Diagnosis == "dependency_regression" {
		t.Fatalf("unregressed comparison produced %q", got.Diagnosis)
	}
}

func TestSynthesizeMakesMissingEvidenceExplicit(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Correlation: api.DebugRequestCorrelation{Stages: []api.DebugRequestCorrelationStage{
			{Phase: "queue", Status: "missing", Reason: "queue signal was not retained"},
		}},
	}

	got := Synthesize(evidence)
	if got.Diagnosis != "insufficient_evidence" || got.Confidence != "low" {
		t.Fatalf("diagnosis = %q/%q, want insufficient_evidence/low", got.Diagnosis, got.Confidence)
	}
	if len(got.Recommendations) == 0 {
		t.Fatal("expected a safe telemetry recommendation")
	}
}
