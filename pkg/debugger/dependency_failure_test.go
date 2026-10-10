package debugger

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseSpansNormalizesStatusAndErrorType(t *testing.T) {
	raw, err := json.Marshal([]StoredSpan{
		{SpanID: "legacy", Name: "db", DurationNanos: 5, Status: "STATUS_CODE_ERROR", ErrorType: "QueryTimeout"},
		{SpanID: "platform", Name: "binding", DurationNanos: 4, Status: "error"},
		{SpanID: "ok", Name: "cache", DurationNanos: 3, Status: "STATUS_CODE_OK", ErrorType: "Ignored"},
		{SpanID: "free-text", Name: "http", DurationNanos: 2, Status: "error", ErrorType: "timeout for alice@example.com"},
		{SpanID: "unknown", Name: "other", DurationNanos: 1, Status: "weird"},
	})
	if err != nil {
		t.Fatal(err)
	}
	spans, _ := ParseSpans(raw)
	got := map[string][2]string{}
	for _, span := range spans {
		got[span.SpanID] = [2]string{span.Status, span.ErrorType}
	}
	want := map[string][2]string{
		"legacy":    {"error", "QueryTimeout"},
		"platform":  {"error", ""},
		"ok":        {"ok", ""},
		"free-text": {"error", ""},
		"unknown":   {"", ""},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: status/error_type = %q, want %q", id, got[id], w)
		}
	}
}

// adr: 934 — a failed dependency call inside a failed request outranks the
// generic request failure.
func TestSynthesizeNamesFailedDependencyCall(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 500},
		Spans: []api.DebugTelemetrySpan{
			{SpanID: "root", Name: "GET /checkout", Status: "error", DurationNanos: 9_000_000},
			{SpanID: "db", Name: "pg.query", Status: "error", ErrorType: "QueryTimeout", DependencyType: AppDependencyType, DependencyKind: "postgresql", DependencyName: "SELECT orders", DurationNanos: 8_000_000},
		},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "dependency_failure" || got.Confidence != "high" {
		t.Fatalf("diagnosis = %q/%q, want dependency_failure/high", got.Diagnosis, got.Confidence)
	}
	want := `The request failed while its call to postgresql "SELECT orders" failed with QueryTimeout.`
	if got.Headline != want {
		t.Fatalf("headline = %q, want %q", got.Headline, want)
	}
	if !hasFinding(got, "dependency_failure") || !hasFinding(got, "request_failure") {
		t.Fatalf("findings = %+v, want dependency_failure and request_failure", got.Findings)
	}
}

func TestSynthesizeIgnoresFailedSpanInSuccessfulRequest(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Spans: []api.DebugTelemetrySpan{
			{SpanID: "cache", Name: "GET", Status: "error", DependencyType: AppDependencyType, DependencyKind: "redis", DependencyName: "GET", DurationNanos: 1_000_000},
		},
	}
	got := Synthesize(evidence)
	if got.Diagnosis == "dependency_failure" || hasFinding(got, "dependency_failure") {
		t.Fatalf("a retried or tolerated failure in a 200 request must not be diagnosed: %+v", got)
	}
}

func TestSynthesizeNamesFailureRegression(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		DependencyComparison: &api.DebugDependencyDeploymentComparison{
			CurrentDeploymentID: "dep-81", PreviousDeploymentID: "dep-80", PreviousDeploymentTag: "v80",
			Dependencies: []api.DebugDependencyLatencyItem{
				{Type: "application", Name: "render", FailureRegression: true},
				{Type: AppDependencyType, Kind: "http", Name: "api.stripe.com", BaselineErrorRatePct: 0, CurrentErrorRatePct: 12.5, FailureRegression: true, TopErrorType: "503"},
			},
		},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "dependency_failure" || got.Confidence != "medium" {
		t.Fatalf("diagnosis = %q/%q, want dependency_failure/medium", got.Diagnosis, got.Confidence)
	}
	want := `http "api.stripe.com" fails 12.5% of calls since the previous deployment (v80), up from 0.0%.`
	if got.Headline != want {
		t.Fatalf("headline = %q, want %q", got.Headline, want)
	}
	if !hasFinding(got, "dependency_failure_regression") {
		t.Fatalf("findings = %+v, want dependency_failure_regression", got.Findings)
	}
}

// A latency regression stays the headline when both signals are present.
func TestSynthesizePrefersLatencyRegressionOverFailureRegression(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		DependencyComparison: &api.DebugDependencyDeploymentComparison{
			Dependencies: []api.DebugDependencyLatencyItem{
				{Type: AppDependencyType, Kind: "postgresql", Name: "SELECT orders", BaselineP95MS: 82, CurrentP95MS: 191, RegressionFactor: 2.33, Regression: true},
				{Type: AppDependencyType, Kind: "http", Name: "api.stripe.com", CurrentErrorRatePct: 12.5, FailureRegression: true},
			},
		},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "dependency_regression" {
		t.Fatalf("diagnosis = %q, want dependency_regression", got.Diagnosis)
	}
	if !hasFinding(got, "dependency_failure_regression") {
		t.Fatalf("findings = %+v, want the failure regression still reported", got.Findings)
	}
}

func hasFinding(explanation api.DebugEvidenceExplanation, code string) bool {
	for _, finding := range explanation.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
