package deploystaging

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type verificationFixture struct {
	MemStore         *state.MemStore
	receipt          api.ProjectSubmissionReceipt
	advances, probes int
	active           string
	round            *state.ProjectReleaseVerificationRound
}

func (f *verificationFixture) ListPendingSafeProjectSubmissions(context.Context) ([]state.SafeProjectSubmission, error) {
	return []state.SafeProjectSubmission{{AccountID: "account", AttemptID: "attempt"}}, nil
}
func (f *verificationFixture) ProjectSubmission(context.Context, string, string) (api.ProjectSubmissionReceipt, error) {
	return f.receipt, nil
}
func (f *verificationFixture) ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error) {
	return state.ProjectReleaseSet{ID: f.active}, nil
}
func (f *verificationFixture) ProjectReleaseSetByID(context.Context, string, string, string, string) (state.ProjectReleaseSet, error) {
	expires := time.Now().Add(time.Hour)
	return state.ProjectReleaseSet{ID: "old", ExpiresAt: &expires, Members: []state.ProjectReleaseMember{{AppID: "app", DeploymentID: "baseline"}}}, nil
}
func (f *verificationFixture) AdvanceProjectReleaseVerification(_ context.Context, _ string, _ string, round *state.ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error) {
	f.advances++
	f.round = round
	return f.receipt.Apply.SafeRelease, nil
}
func TestProjectVerificationWorkerRecoveryAndArtifactGuard(t *testing.T) {
	for _, tc := range []struct {
		name, status                           string
		expired, artifactFailure, probeFailure bool
		active                                 string
		advances, checks, probes               int
		wantErr                                bool
	}{
		{"probes", "verifying", false, false, false, "new", 1, 0, 1, false},
		{"probe unavailable", "verifying", false, false, true, "new", 1, 0, 1, false},
		{"deadline", "verifying", true, false, false, "new", 1, 0, 0, false},
		{"operator changed graph", "rolling_back", false, false, false, "operator", 1, 0, 0, false},
		{"rollback artifacts missing", "rolling_back", false, true, false, "new", 0, 1, 0, true},
		{"rollback ready", "rolling_back", false, false, false, "new", 1, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.Now().Add(time.Hour)
			if tc.expired {
				deadline = time.Now().Add(-time.Second)
			}
			fixture := &verificationFixture{MemStore: state.NewMemStore(), active: tc.active, receipt: api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{Status: tc.status, ReleaseID: "new", PreviousReleaseID: "old", Verification: &api.ProjectReleaseVerification{Deadline: &deadline, Probes: []api.ProjectReleaseProbe{{AppID: "app", Method: "GET", Path: "/health"}}}}}}}}
			checks := 0
			err := ReconcileSafeProjectReleases(t.Context(), statetest.NewMem(fixture), func(_ context.Context, members []state.ProjectReleaseMember) error {
				checks++
				if len(members) != 1 || members[0].DeploymentID != "baseline" {
					t.Fatal("preflight did not check previous graph")
				}
				if tc.artifactFailure {
					return errors.New("artifact missing")
				}
				return nil
			}, func(context.Context, api.ProjectSubmissionReceipt, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error) {
				fixture.probes++
				if tc.probeFailure {
					return nil, errors.New("delivery unavailable")
				}
				return []api.ProjectReleaseProbeEvidence{{CandidateRequests: 10}}, nil
			})
			if fixture.advances != tc.advances || fixture.probes != tc.probes || checks != tc.checks || (err != nil) != tc.wantErr {
				t.Fatalf("advance=%d probes=%d checks=%d err=%v", fixture.advances, fixture.probes, checks, err)
			}
			if tc.probeFailure && (fixture.round == nil || len(fixture.round.Evidence) != 0) {
				t.Fatal("unavailable probes became health evidence")
			}
		})
	}
}
