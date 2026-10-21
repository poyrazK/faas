package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectMigrationMemory(t *testing.T) {
	testProjectMigration(t, func(*testing.T) state.Store { return state.NewMemStore() })
}

func testProjectMigration(t *testing.T, factory func(*testing.T) state.Store) {
	for _, outcome := range []string{"missing", "queued", "running", "success", "failed", "uncertain", "transport", "restore_replay", "wrong_command", "operator", "parallel"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := t.Context()
			store := factory(t)
			account, err := store.CreateAccount(ctx, "migration@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "migration"})
			if err != nil {
				t.Fatal(err)
			}
			var apps []state.App
			var members []state.ProjectReleaseMember
			for _, name := range []string{"migration-api", "migration-web"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP, RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
				if err != nil {
					t.Fatal(err)
				}
				old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
					t.Fatal(err)
				}
				apps = append(apps, app)
				members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: old.ID})
			}
			publisher := store.(state.ProjectReleaseSetStore)
			reader := store.(state.ProjectReleaseSetReader)
			baseline, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members)
			if err != nil {
				t.Fatal(err)
			}
			command := []string{"/app/migrate", "--expand"}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{{AppID: apps[0].ID, Slug: apps[0].Slug, Method: "GET", Path: "/health"}}}, ReleaseCommand: &api.ProjectReleaseCommand{AppID: apps[0].ID, Slug: apps[0].Slug, CommandHash: api.ProjectReleaseCommandHash(command, false), Compatibility: "backward-compatible", Status: "waiting"}}
			receipts := store.(state.ProjectSubmissionStore)
			gates := store.(state.ProjectReleaseCommandStore)
			attempt := strings.Repeat("a", 32)
			receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", Status: "processing", Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			if _, err := receipts.ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("b", 64), receipt); err != nil {
				t.Fatal(err)
			}
			if err := gates.StageProjectReleaseCommand(ctx, account.ID, attempt, project.ID, safe); err != nil {
				t.Fatal(err)
			}
			if err := gates.StageProjectReleaseCommand(ctx, account.ID, attempt, project.ID, safe); err != nil {
				t.Fatalf("stage replay: %v", err)
			}
			if outcome == "parallel" {
				other := receipt
				other.AttemptID = strings.Repeat("c", 32)
				if _, err := receipts.ReserveProjectSubmission(ctx, account.ID, other.AttemptID, strings.Repeat("d", 64), other); err != nil {
					t.Fatal(err)
				}
				if err := gates.StageProjectReleaseCommand(ctx, account.ID, other.AttemptID, project.ID, safe); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("parallel admission=%v", err)
				}
				return
			}
			var candidate state.Deployment
			for i, app := range apps {
				now := time.Now()
				stages, _ := json.Marshal(state.StageState{Current: state.StageReadiness, CurrentStartedAt: &now})
				dep := state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:migration", TrafficPercentExplicit: true, StageState: stages}
				if i == 0 {
					dep.ReleaseCommand = command
				}
				created, err := store.CreateDeployment(ctx, dep)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentRootfs(ctx, created.ID, "/migration.ext4", "layers/"+created.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, created.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.CloseDeploymentStage(ctx, created.ID, state.StageReadiness, time.Now()); err != nil {
					t.Fatal(err)
				}
				receipt.Apply.Builds = append(receipt.Apply.Builds, api.AppliedBuild{AppID: app.ID, Slug: app.Slug, DeploymentID: created.ID, BuildID: "accepted"})
				if i == 0 {
					candidate = created
				}
			}
			tasks := store.(state.AppTaskStore)
			var task state.AppTask
			now := time.Now().UTC()
			if outcome != "missing" {
				requested := command
				if outcome == "wrong_command" {
					requested = []string{"/app/other"}
				}
				task, err = tasks.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account.ID, AppID: apps[0].ID, DeploymentID: candidate.ID, Kind: state.AppTaskKindRelease, Command: requested, CreatedAt: now.Add(-time.Second)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tasks.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account.ID, AppID: apps[0].ID, DeploymentID: candidate.ID, Kind: state.AppTaskKindRelease, Command: requested, CreatedAt: now.Add(-time.Second)}); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("duplicate task admission=%v", err)
				}
			}
			if outcome == "restore_replay" || outcome == "running" || outcome == "success" || outcome == "failed" || outcome == "uncertain" || outcome == "transport" || outcome == "operator" {
				task, err = tasks.ClaimNextAppTask(ctx, "migration-test", now, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "restore_replay" {
					if _, err := tasks.SweepExpiredAppTasks(ctx, now.Add(time.Minute)); err != nil {
						t.Fatal(err)
					}
					replay, err := tasks.ClaimNextAppTask(ctx, "recovered", now.Add(2*time.Minute), 30*time.Second)
					if err != nil || replay.ID != task.ID {
						t.Fatalf("restore replay=%+v %v", replay, err)
					}
				} else {
					task, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, now.Add(time.Second))
					if err != nil {
						t.Fatal(err)
					}
					if outcome == "uncertain" {
						if _, err := tasks.SweepExpiredAppTasks(ctx, now.Add(time.Minute)); err != nil {
							t.Fatal(err)
						}
						if _, err := tasks.ClaimNextAppTask(ctx, "recovered", now.Add(2*time.Minute), 30*time.Second); !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("uncertain execution was replayed: %v", err)
						}
					} else if outcome != "running" {
						status := state.AppTaskSucceeded
						exit := 0
						params := state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: status, ExitCode: &exit, StdoutTail: "migration-output-not-in-receipt", FinishedAt: now.Add(2 * time.Second)}
						if outcome == "failed" {
							exit = 1
							params.Status = state.AppTaskFailed
							code, message := "command_failed", "migration exited nonzero"
							params.FailureCode = &code
							params.FailureMessage = &message
						}
						if outcome == "transport" {
							params.Status = state.AppTaskFailed
							params.ExitCode = nil
							code, message := "execution_transport_failed", "result lost"
							params.FailureCode = &code
							params.FailureMessage = &message
						}
						if _, err := tasks.CompleteAppTask(ctx, params); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			receipt.SelectionComplete = true
			receipt.Status = "complete"
			if err := receipts.WriteProjectSubmission(ctx, account.ID, receipt); err != nil {
				t.Fatal(err)
			}
			if outcome == "operator" {
				if _, err := publisher.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members); err != nil {
					t.Fatal(err)
				}
			}
			progress, err := store.(state.ProjectSafeReleaseStore).ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			expected := "building"
			if outcome == "success" {
				expected = "verifying"
			}
			if outcome == "failed" || outcome == "uncertain" || outcome == "transport" {
				expected = "failed"
			}
			if outcome == "wrong_command" || outcome == "operator" {
				expected = "conflicted"
			}
			if progress.Status != expected {
				t.Fatalf("progress=%+v command=%+v", progress, progress.ReleaseCommand)
			}
			if (outcome == "uncertain" || outcome == "transport") && progress.ReleaseCommand.Status != "uncertain" {
				t.Fatalf("missing uncertainty: %+v", progress.ReleaseCommand)
			}
			if outcome == "success" && progress.ReleaseCommand.Status != "succeeded" {
				t.Fatalf("missing success evidence: %+v", progress.ReleaseCommand)
			}
			active, err := reader.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if outcome != "success" && outcome != "operator" && active.ID != baseline.ID {
				t.Fatal("migration cutover occurred without success")
			}
			if outcome == "success" && active.ID == baseline.ID {
				t.Fatal("successful migration did not activate")
			}
			for i := 0; i < 2; i++ {
				again, err := store.(state.ProjectSafeReleaseStore).ReconcileSafeProjectSubmission(ctx, account.ID, attempt)
				if err != nil || again.ReleaseID != progress.ReleaseID || again.ReleaseCommand.TaskID != progress.ReleaseCommand.TaskID {
					t.Fatalf("recovery changed identity: %+v %v", again, err)
				}
			}
			restored, err := receipts.ProjectSubmission(ctx, account.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(restored)
			if strings.Contains(string(encoded), "migration-output-not-in-receipt") {
				t.Fatal("task output leaked into receipt")
			}
			if outcome == "failed" || outcome == "uncertain" {
				retained, err := tasks.AppTaskByID(ctx, account.ID, apps[0].ID, task.ID)
				if err != nil || !retained.Status.Terminal() {
					t.Fatalf("missing durable terminal task: %+v %v", retained, err)
				}
			}
		})
	}
}
