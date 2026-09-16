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
