package pgintegration_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsCurrentWorkloadProgressTracksCandidateBuildFailure(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, lease, app, previous, plan := appliedSourceCandidateFixture(t, basic, "source")
		preparer := basic.(state.EnvironmentGitOpsPreparationStore)
		requests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
		if err != nil || len(requests) != 1 {
			t.Fatalf("source request: %+v %v", requests, err)
		}
		candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan,
			map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
		if err != nil || len(candidates) != 1 {
			t.Fatalf("source candidate: %+v %v", candidates, err)
		}
		assertProgress := func(build state.BuildStatus, preparation string) {
			t.Helper()
			current, err := basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(t.Context(), lease.Source.AccountID, lease.Source.ID)
			if err != nil || current == nil || len(current.Workloads) != 1 || current.CommitSHA != lease.Revision.CommitSHA {
				t.Fatalf("current progress: %+v %v", current, err)
			}
			row := current.Workloads[0]
			if row.DeploymentID != candidates[0].DeploymentID || row.AppID != app.ID || row.BuildStatus != string(build) ||
				row.Preparation != preparation || row.Qualification == "complete" || row.Activation == "complete" || row.Serving != "unknown" {
				t.Fatalf("build became qualification or serving: %+v", row)
			}
			if slices.Contains(row.BlockingReasons, "environment_workload_build_failed") != (build == state.BuildFailed) {
				t.Fatalf("build failure not scoped to candidate: %+v", row)
			}
			encoded, err := json.Marshal(row)
			if err != nil || strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "build.log") {
				t.Fatalf("build diagnostics exposed logs or failure text: %s %v", encoded, err)
			}
		}
		assertProgress(state.BuildQueued, "pending")
		claim, err := basic.ClaimQueuedBuild(t.Context(), candidates[0].BuildID)
		if err != nil {
			t.Fatal(err)
		}
		assertProgress(state.BuildRunning, "pending")
		if err := basic.FailBuild(t.Context(), claim, state.FailureUserError, "PRIVATE build output"); err != nil {
			t.Fatal(err)
		}
		assertProgress(state.BuildFailed, "failed")
		live, err := basic.LiveDeploymentForScope(t.Context(), app.ID, "production")
		if err != nil || live.ID != previous.ID {
			t.Fatalf("diagnostic read changed serving deployment: %+v %v", live, err)
		}
	})
}
