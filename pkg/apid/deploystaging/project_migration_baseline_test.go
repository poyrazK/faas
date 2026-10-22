package deploystaging

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type migrationBaselineFixture struct {
	safeReleaseFixture
	task   state.AppTask
	active string
	rounds []state.ProjectMigrationBaselineRound
}

func (f *migrationBaselineFixture) ReleaseAppTaskByDeployment(context.Context, string) (state.AppTask, error) {
	return f.task, nil
}
func (f *migrationBaselineFixture) ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error) {
	return state.ProjectReleaseSet{ID: f.active, Members: []state.ProjectReleaseMember{{AppID: "app", DeploymentID: "baseline-deployment"}}}, nil
}
func (f *migrationBaselineFixture) RecordProjectMigrationBaseline(_ context.Context, _, _ string, round state.ProjectMigrationBaselineRound) (*api.ProjectSafeRelease, error) {
	f.rounds = append(f.rounds, round)
	return f.receipt.Apply.SafeRelease, nil
}

func TestMigrationBaselineController(t *testing.T) {
	for _, scenario := range []string{"success", "running", "missing_exit", "new_owner", "probe_error", "no_probe", "baseline_artifact_error", "cancelled", "recent_unknown", "failed_checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			finished := now.Add(-time.Second)
			exit := 0
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "baseline", Deadline: now.Add(time.Hour), Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: "app", Slug: "api", Method: "GET", Path: "/database-health"}}}, ReleaseCommand: &api.ProjectReleaseCommand{AppID: "app", VerifyBaseline: true}}
			stage, _ := json.Marshal(state.StageState{History: []state.StageStateItem{{Name: state.StageReadiness, Status: "completed"}}})
			f := &migrationBaselineFixture{safeReleaseFixture: safeReleaseFixture{receipt: api.ProjectSubmissionReceipt{Status: "complete", SelectionComplete: true, Apply: api.ApplyResponse{ProjectID: "project", PlanResponse: api.PlanResponse{SafeRelease: safe}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}, deployment: state.Deployment{AppID: "app", Status: state.DeployLive, TrafficPercentExplicit: true, StageState: stage}}, task: state.AppTask{ID: "migration-task", Status: state.AppTaskSucceeded, ExitCode: &exit, FinishedAt: &finished}, active: "baseline"}
			switch scenario {
			case "running":
				f.task.Status = state.AppTaskRunning
			case "missing_exit":
				f.task.ExitCode = nil
			case "new_owner":
				f.active = "new-release"
			case "recent_unknown", "failed_checkpoint":
				status := "unknown"
				if scenario == "failed_checkpoint" {
					status = "failed"
				}
				safe.ReleaseCommand.BaselineVerification = &api.ProjectMigrationBaselineVerification{TaskID: f.task.ID, CheckedAt: now, Status: status}
			}
			checks, probed := 0, 0
			check := func(_ context.Context, members []state.ProjectReleaseMember) error {
				checks++
				if checks == 2 {
					if members[0].DeploymentID != "baseline-deployment" {
						t.Fatal("wrong baseline artifact")
					}
					if scenario == "baseline_artifact_error" {
						return errors.New("baseline unavailable")
					}
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe := func(_ context.Context, _ api.ProjectSubmissionReceipt, baseline state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error) {
				probed++
				if baseline.ID != "baseline" {
					t.Fatal("wrong production graph probed")
				}
				if scenario == "cancelled" {
					cancel()
					return nil, ctx.Err()
				}
				if scenario == "probe_error" {
					return nil, errors.New("gateway unavailable")
				}
				return []api.ProjectReleaseProbeEvidence{{Probe: safe.Verification.Probes[0], StableRequests: api.RouteHealthProbeRequestsPerMinute}}, nil
			}
			if scenario == "no_probe" {
				probe = nil
			}
			err := ReconcileSafeProjectReleases(ctx, statetest.NewMem(f), check, probe)
			skip := scenario == "running" || scenario == "missing_exit" || scenario == "new_owner" || scenario == "recent_unknown" || scenario == "failed_checkpoint"
			if scenario == "cancelled" {
				if !errors.Is(err, context.Canceled) || len(f.rounds) != 0 || f.reconciled != 0 {
					t.Fatalf("cancelled checkpoint: %v %+v", err, f.rounds)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if skip {
				if probed != 0 || len(f.rounds) != 0 {
					t.Fatal("probe before task completion or after ownership changed")
				}
				return
			}
			if len(f.rounds) != 1 || f.rounds[0].TaskID != f.task.ID || f.rounds[0].StartedAt.Before(finished) {
				t.Fatalf("missing post-expansion attribution: %+v", f.rounds)
			}
			unknown := scenario == "probe_error" || scenario == "no_probe" || scenario == "baseline_artifact_error"
			if unknown && len(f.rounds[0].Evidence) != 0 {
				t.Fatal("unknown evidence counted as healthy")
			}
			if f.reconciled != 1 {
				t.Fatal("publication gate was not reconciled")
			}
		})
	}
}
