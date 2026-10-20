package deploystaging

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"testing"
	"time"
)

type safeReleaseFixture struct {
	MemStore   *state.MemStore
	receipt    api.ProjectSubmissionReceipt
	deployment state.Deployment
	reconciled int
}

func (s *safeReleaseFixture) ListPendingSafeProjectSubmissions(context.Context) ([]state.SafeProjectSubmission, error) {
	return []state.SafeProjectSubmission{{AccountID: "account", AttemptID: "attempt"}}, nil
}
func (s *safeReleaseFixture) ProjectSubmission(context.Context, string, string) (api.ProjectSubmissionReceipt, error) {
	return s.receipt, nil
}
func (s *safeReleaseFixture) DeploymentByID(context.Context, string) (state.Deployment, error) {
	return s.deployment, nil
}
func (s *safeReleaseFixture) ReconcileSafeProjectSubmission(context.Context, string, string) (*api.ProjectSafeRelease, error) {
	s.reconciled++
	return s.receipt.Apply.SafeRelease, nil
}
func TestSafeProjectReleaseArtifactPreflight(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		ready, expired, rejected bool
		checks, reconciled       int
	}{
		{"ready", true, false, false, 1, 1},
		{"artifact rejected", true, false, true, 1, 0},
		{"still building", false, false, false, 0, 1},
		{"expired", true, true, true, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.Now().Add(time.Hour)
			if tc.expired {
				deadline = time.Now().Add(-time.Minute)
			}
			stage, _ := json.Marshal(state.StageState{History: []state.StageStateItem{{Name: state.StageReadiness, Status: "completed"}}})
			fixture := &safeReleaseFixture{receipt: api.ProjectSubmissionReceipt{Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Deadline: deadline}}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}, deployment: state.Deployment{AppID: "app", Status: state.DeployLive, TrafficPercentExplicit: true, StageState: stage}}
			if !tc.ready {
				fixture.deployment.Status = state.DeployBuilding
			}
			checks := 0
			err := ReconcileSafeProjectReleases(t.Context(), statetest.NewMem(fixture), func(_ context.Context, members []state.ProjectReleaseMember) error {
				checks++
				if len(members) != 1 || members[0].DeploymentID != "candidate" {
					t.Fatal("wrong artifact preflight")
				}
				if tc.rejected {
					return errors.New("unavailable artifact")
				}
				return nil
			})
			if checks != tc.checks || fixture.reconciled != tc.reconciled || (err != nil) != (tc.rejected && !tc.expired) {
				t.Fatalf("checks=%d reconciled=%d err=%v", checks, fixture.reconciled, err)
			}
		})
	}
}
