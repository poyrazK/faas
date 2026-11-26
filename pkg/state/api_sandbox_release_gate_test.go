package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAPISandboxReleaseGateRequiresCompletionBeforeDeadline(t *testing.T) {
	baseline := Deployment{ID: "baseline", AppID: "app", ImageDigest: "sha256:baseline"}
	candidate := Deployment{ID: "candidate", AppID: "app", ImageDigest: "sha256:candidate"}
	deadline := time.Now().UTC()
	run := APISandboxSuiteRun{
		AppID: "app",
		Pins:  APISandboxApprovalPins{Baseline: APISandboxArtifactPin(baseline), Candidate: APISandboxArtifactPin(candidate)},
		APISandboxSuiteRunSummary: api.APISandboxSuiteRunSummary{
			BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID,
			SuiteRevision: "suite", CompletedCount: 1, ScenarioCount: 1,
		},
	}
	for _, tt := range []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "before", at: deadline.Add(-time.Millisecond), want: "passed"},
		{name: "at cutoff", at: deadline, want: "failed"},
		{name: "after", at: deadline.Add(time.Millisecond), want: "failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run.FinishedAt = &tt.at
			if got := sandboxSuiteGateStatus(run, "suite", baseline, candidate, deadline, true); got != tt.want {
				t.Fatalf("gate status = %q, want %q", got, tt.want)
			}
		})
	}
}
