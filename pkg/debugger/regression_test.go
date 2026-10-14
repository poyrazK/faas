package debugger

// adr: 127 — error-rate route regressions (amendment): thresholds and kinds.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestErrorRateRegressed(t *testing.T) {
	for name, tc := range map[string]struct {
		baseline, current RouteErrors
		want              bool
	}{
		"new failures":                 {RouteErrors{200, 0}, RouteErrors{200, 30}, true},
		"too few failed requests":      {RouteErrors{200, 0}, RouteErrors{100, 9}, false},
		"too little baseline traffic":  {RouteErrors{49, 0}, RouteErrors{200, 30}, false},
		"too little current traffic":   {RouteErrors{200, 0}, RouteErrors{49, 30}, false},
		"rise under five points":       {RouteErrors{1000, 20}, RouteErrors{1000, 60}, false},
		"rise above five but under 2x": {RouteErrors{1000, 100}, RouteErrors{1000, 160}, false},
		"rate doubled and +5 points":   {RouteErrors{1000, 50}, RouteErrors{1000, 110}, true},
		"steady failure rate":          {RouteErrors{1000, 300}, RouteErrors{1000, 300}, false},
		"improvement":                  {RouteErrors{1000, 300}, RouteErrors{1000, 10}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ErrorRateRegressed(tc.baseline, tc.current); got != tc.want {
				t.Fatalf("ErrorRateRegressed(%+v, %+v) = %v, want %v", tc.baseline, tc.current, got, tc.want)
			}
		})
	}
}

func TestRouteErrorsRatePct(t *testing.T) {
	for _, tc := range []struct {
		in   RouteErrors
		want float64
	}{
		{RouteErrors{0, 0}, 0},
		{RouteErrors{3, 1}, 33.33},
		{RouteErrors{8, 1}, 12.5},
		// Errors cannot exceed requests; a malformed count saturates at 100%.
		{RouteErrors{5, 9}, 100},
	} {
		if got := tc.in.RatePct(); got != tc.want {
			t.Errorf("%+v.RatePct() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRegressionKind(t *testing.T) {
	cases := []struct {
		latency, errors       bool
		want                  string
		hasErrors, hasLatency bool
	}{
		{false, false, "", false, true},
		{true, false, RegressionKindLatency, false, true},
		{false, true, RegressionKindErrors, true, false},
		{true, true, RegressionKindLatencyAndErrors, true, true},
	}
	for _, tc := range cases {
		got := RegressionKind(tc.latency, tc.errors)
		if got != tc.want || RegressionHasErrors(got) != tc.hasErrors || RegressionHasLatency(got) != tc.hasLatency {
			t.Errorf("RegressionKind(%v, %v) = %q (errors=%v latency=%v)", tc.latency, tc.errors, got, RegressionHasErrors(got), RegressionHasLatency(got))
		}
	}
}

func TestSynthesizeNamesErrorRegression(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindErrors, Factor: "1.00",
			ErrorRatePct: 12.5, ErrorRateBasePct: 0.4, ErrorCount: 61},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "error_regression" || got.Confidence != "high" {
		t.Fatalf("diagnosis = %q/%q, want error_regression/high", got.Diagnosis, got.Confidence)
	}
	if want := "This route fails 12.5% of requests since the deployment, up from 0.4%."; got.Headline != want {
		t.Fatalf("headline = %q, want %q", got.Headline, want)
	}
	if !hasFinding(got, "error_regression") || hasFinding(got, "performance_regression") {
		t.Fatalf("an errors-only regression reports only the error finding: %+v", got.Findings)
	}
}

func TestSynthesizeLatencyAndErrorsReportsBoth(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindLatencyAndErrors, Factor: "2.10",
			P95MS: 420, P95BaseMS: 200, AffectedCount: 150, ErrorRatePct: 9, ErrorRateBasePct: 1, ErrorCount: 45},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "error_regression" {
		t.Fatalf("diagnosis = %q, want error_regression (failures outrank slowness)", got.Diagnosis)
	}
	if !hasFinding(got, "error_regression") || !hasFinding(got, "performance_regression") {
		t.Fatalf("findings = %+v, want both regression findings", got.Findings)
	}
}

// A regression stored before error-rate detection has no kind and stays a
// latency regression.
func TestSynthesizeLegacyRegressionIsLatency(t *testing.T) {
	got := Synthesize(api.DebugRequestEvidenceResponse{
		Request:    api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "GET /", Factor: "1.50", P95MS: 300, P95BaseMS: 200},
	})
	if got.Diagnosis != "performance_regression" || hasFinding(got, "error_regression") {
		t.Fatalf("legacy regression = %q %+v", got.Diagnosis, got.Findings)
	}
}

// For a route that regressed on errors, a failing dependency outranks a slow
// one, matching the detector's suspect.
func TestSynthesizeErrorRegressionPrefersFailingDependency(t *testing.T) {
	comparison := &api.DebugDependencyDeploymentComparison{
		PreviousDeploymentTag: "v80",
		Dependencies: []api.DebugDependencyLatencyItem{
			{Type: AppDependencyType, Kind: "postgresql", Name: "SELECT orders", BaselineP95MS: 82, CurrentP95MS: 191, RegressionFactor: 2.33, Regression: true},
			{Type: AppDependencyType, Kind: "http", Name: "api.stripe.com", BaselineErrorRatePct: 0, CurrentErrorRatePct: 12.5, FailureRegression: true},
		},
	}
	errorsRoute := Synthesize(api.DebugRequestEvidenceResponse{
		Request:              api.DebugTelemetryRequestItem{Status: 200},
		Regression:           &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindErrors, Factor: "1.00", ErrorRatePct: 12.5},
		DependencyComparison: comparison,
	})
	if errorsRoute.Diagnosis != "dependency_failure" || errorsRoute.Confidence != "high" {
		t.Fatalf("errors route diagnosis = %q/%q, want dependency_failure/high", errorsRoute.Diagnosis, errorsRoute.Confidence)
	}
	latencyRoute := Synthesize(api.DebugRequestEvidenceResponse{
		Request:              api.DebugTelemetryRequestItem{Status: 200},
		Regression:           &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindLatency, Factor: "2.10"},
		DependencyComparison: comparison,
	})
	if latencyRoute.Diagnosis != "dependency_regression" {
		t.Fatalf("latency route diagnosis = %q, want dependency_regression", latencyRoute.Diagnosis)
	}
}

// A route that only started failing is not explained by a slower dependency.
func TestSynthesizeErrorsOnlyRouteIgnoresSlowDependencyAsDiagnosis(t *testing.T) {
	got := Synthesize(api.DebugRequestEvidenceResponse{
		Request:    api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindErrors, Factor: "1.00", ErrorRatePct: 12.5, ErrorRateBasePct: 0.4},
		DependencyComparison: &api.DebugDependencyDeploymentComparison{Dependencies: []api.DebugDependencyLatencyItem{
			{Type: AppDependencyType, Kind: "postgresql", Name: "SELECT orders", BaselineP95MS: 82, CurrentP95MS: 191, RegressionFactor: 2.33, Regression: true},
		}},
	})
	if got.Diagnosis != "error_regression" || !hasFinding(got, "dependency_regression") {
		t.Fatalf("diagnosis = %q findings %+v, want error_regression with the slow dependency still listed", got.Diagnosis, got.Findings)
	}
}
