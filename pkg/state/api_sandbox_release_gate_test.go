package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAPISandboxReleaseGateRequiresCompletionBeforeDeadline(t *testing.T) {
	baseline := Deployment{ID: "baseline", AppID: "app", ImageDigest: "sha256:baseline"}
	candidate := Deployment{ID: "candidate", AppID: "app", ImageDigest: "sha256:candidate"}
	cutoff := time.Date(2026, time.November, 26, 12, 0, 0, 0, time.UTC)
	later := cutoff.Add(10 * time.Minute)
	run := APISandboxSuiteRun{
		AppID: "app",
		Pins:  APISandboxApprovalPins{Baseline: APISandboxArtifactPin(baseline), Candidate: APISandboxArtifactPin(candidate)},
		APISandboxSuiteRunSummary: api.APISandboxSuiteRunSummary{
			BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID,
			SuiteRevision: "suite", CompletedCount: 1, ScenarioCount: 1,
		},
	}
	for _, tt := range []struct {
		name             string
		at               time.Time
		suiteDeadline    time.Time
		approvalDeadline time.Time
		want             string
	}{
		{name: "before approval cutoff", at: cutoff.Add(-time.Millisecond), suiteDeadline: later, approvalDeadline: cutoff, want: "passed"},
		{name: "at approval cutoff", at: cutoff, suiteDeadline: later, approvalDeadline: cutoff, want: "failed"},
		{name: "before suite cutoff", at: cutoff.Add(-time.Millisecond), suiteDeadline: cutoff, approvalDeadline: later, want: "passed"},
		{name: "after suite cutoff", at: cutoff.Add(time.Millisecond), suiteDeadline: cutoff, approvalDeadline: later, want: "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run.FinishedAt = &tt.at
			run.DeadlineAt = tt.suiteDeadline
			deadline := run.evidenceDeadline(tt.approvalDeadline)
			if got := sandboxSuiteGateStatus(run, "suite", baseline, candidate, deadline, true); got != tt.want {
				t.Fatalf("gate status = %q, want %q", got, tt.want)
			}
		})
	}
}
