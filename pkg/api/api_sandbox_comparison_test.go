package api

import (
	"testing"
	"time"
)

func TestAPISandboxComparisonVerdicts(t *testing.T) {
	passed := APISandboxRunReport{Status: APISandboxRunPassed}
	assertion := APISandboxRunReport{
		Status: APISandboxRunFailed, FailureCode: "json_assertion_failed",
		Steps: []APISandboxRunStep{{Index: 1}, {Index: 2, FailureCode: "json_assertion_failed", FailedPointer: "/state"}},
	}
	transport := APISandboxRunReport{
		Status: APISandboxRunFailed, FailureCode: "request_unavailable",
		Steps: []APISandboxRunStep{{Index: 1, FailureCode: "request_unavailable"}},
	}
	interrupted := APISandboxRunReport{Status: APISandboxRunInterrupted, FailureCode: "worker_interrupted"}
	for _, tc := range []struct {
		name                  string
		baseline, candidate   APISandboxRunReport
		verdict, side, reason string
		step                  int
	}{
		{"pending", passed, APISandboxRunReport{Status: APISandboxRunQueued}, APISandboxComparisonRunning, "", "", 0},
		{"both pass", passed, passed, APISandboxComparisonPassed, "", "", 0},
		{"candidate regressed", passed, assertion, APISandboxComparisonRegression, "candidate", "candidate_check_failed", 2},
		{"baseline failed", assertion, passed, APISandboxComparisonInconclusive, "baseline", "baseline_check_failed", 2},
		{"both fail", assertion, assertion, APISandboxComparisonInconclusive, "baseline", "baseline_check_failed", 2},
		{"candidate unreachable", passed, transport, APISandboxComparisonInconclusive, "candidate", "candidate_unavailable", 1},
		{"baseline interrupted", interrupted, passed, APISandboxComparisonInconclusive, "baseline", "baseline_unavailable", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.baseline.SourceDeploymentID = "baseline-release"
			tc.candidate.SourceDeploymentID, tc.candidate.ScenarioRevision = "candidate-release", "scenario-revision"
			got := CompareAPISandboxRuns("comparison", time.Now(), tc.baseline, tc.candidate)
			if got.Verdict != tc.verdict || got.FailureSide != tc.side || got.Reason != tc.reason ||
				got.BaselineDeploymentID != "baseline-release" || got.CandidateDeploymentID != "candidate-release" ||
				got.ScenarioRevision != "scenario-revision" {
				t.Fatalf("unexpected verdict: %+v", got)
			}
			if (got.FirstFailingStep == nil && tc.step != 0) ||
				(got.FirstFailingStep != nil && got.FirstFailingStep.Index != tc.step) {
				t.Fatalf("unexpected first failing step: %+v", got.FirstFailingStep)
			}
		})
	}
}
