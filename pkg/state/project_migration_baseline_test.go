package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMigrationBaselineMemory(t *testing.T) {
	testMigrationBaseline(t, func(*testing.T) Store { return NewMemStore() })
}

func testMigrationBaseline(t *testing.T, factory func(*testing.T) Store) {
	for _, outcome := range []string{"healthy", "failed", "unknown", "unauthenticated", "wrong_route", "wrong_task", "before_task", "operator", "operator_before_proof"} {
		t.Run(outcome, func(t *testing.T) {
			store := factory(t)
			ctx := t.Context()
			a, err := store.CreateAccount(ctx, "baseline@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			p, err := store.CreateProject(ctx, Project{AccountID: a.ID, Slug: "baseline"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, App{AccountID: a.ID, ProjectID: p.ID, Slug: "baseline-api", WorkloadName: "api", WorkloadClass: WorkloadClassHTTP, RAMMB: 256, MaxConcurrency: 2, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
			if err != nil {
				t.Fatal(err)
			}
			old, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDeploymentLive(ctx, old.ID); err != nil {
				t.Fatal(err)
			}
			publisher := store.(ProjectReleaseSetStore)
			reader := store.(ProjectReleaseSetReader)
			members := []ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}
			baseline, err := publisher.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, members)
			if err != nil {
				t.Fatal(err)
			}
			command := []string{"/app/migrate", "expand"}
			probe := api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: "GET", Path: "/database-health"}
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour), Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{probe}}, ReleaseCommand: &api.ProjectReleaseCommand{AppID: app.ID, Slug: app.Slug, CommandHash: api.ProjectReleaseCommandHash(command, false), Compatibility: "backward-compatible", Status: "waiting", VerifyBaseline: true}}
			attempt := strings.Repeat("a", 32)
			r := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: p.Slug, Environment: "production", Status: "processing", Apply: api.ApplyResponse{ProjectID: p.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
			subs := store.(ProjectSubmissionStore)
			if _, err = subs.ReserveProjectSubmission(ctx, a.ID, attempt, strings.Repeat("b", 64), r); err != nil {
				t.Fatal(err)
			}
			if err = store.(ProjectReleaseCommandStore).StageProjectReleaseCommand(ctx, a.ID, attempt, p.ID, safe); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(-10 * time.Second)
			stages, _ := json.Marshal(StageState{Current: StageReadiness, CurrentStartedAt: &now})
			candidate, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", Kind: DeploymentKindTarball, ImageDigest: "sha256:migration", TrafficPercentExplicit: true, StageState: stages, ReleaseCommand: command})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.SetDeploymentRootfs(ctx, candidate.ID, "/migration.ext4", "layers/"+candidate.ID, 4096); err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.CloseDeploymentStage(ctx, candidate.ID, StageReadiness, now); err != nil {
				t.Fatal(err)
			}
			tasks := store.(AppTaskStore)
			task, err := tasks.CreateAppTask(ctx, CreateAppTaskParams{AccountID: a.ID, AppID: app.ID, DeploymentID: candidate.ID, Kind: AppTaskKindRelease, Command: command, CreatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			task, err = tasks.ClaimNextAppTask(ctx, "test", now.Add(time.Second), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			task, err = tasks.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, now.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			exit := 0
			task, err = tasks.CompleteAppTask(ctx, CompleteAppTaskParams{ID: task.ID, LeaseToken: *task.LeaseToken, Status: AppTaskSucceeded, ExitCode: &exit, FinishedAt: now.Add(3 * time.Second), StdoutTail: "private migration output"})
			if err != nil {
				t.Fatal(err)
			}
			r.Apply.Builds = []api.AppliedBuild{{AppID: app.ID, Slug: app.Slug, DeploymentID: candidate.ID, BuildID: "accepted"}}
			r.Status = "complete"
			r.SelectionComplete = true
			if err = subs.WriteProjectSubmission(ctx, a.ID, r); err != nil {
				t.Fatal(err)
			}
			gate := store.(ProjectSafeReleaseStore)
			progress, err := gate.ReconcileSafeProjectSubmission(ctx, a.ID, attempt)
			if err != nil || progress.Status != "building" {
				t.Fatalf("success without baseline evidence activated: %+v %v", progress, err)
			}
			round := ProjectMigrationBaselineRound{TaskID: task.ID, StartedAt: time.Now().UTC(), Evidence: []api.ProjectReleaseProbeEvidence{{Probe: probe, StableRequests: api.RouteHealthProbeRequestsPerMinute}}}
			switch outcome {
			case "failed":
				round.Evidence[0].StableErrors = 1
			case "unknown":
				round.Evidence = nil
			case "unauthenticated":
				round.Evidence[0].Unauthenticated = 1
			case "wrong_route":
				round.Evidence[0].Probe.Path = "/other"
			case "wrong_task":
				round.TaskID = "other"
			case "before_task":
				round.StartedAt = task.FinishedAt.Add(-time.Second)
			}
			recorder := store.(ProjectMigrationBaselineStore)
			if outcome == "operator_before_proof" {
				if _, err = publisher.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, members); err != nil {
					t.Fatal(err)
				}
			}

			_, err = recorder.RecordProjectMigrationBaseline(ctx, a.ID, attempt, round)
			invalid := outcome == "wrong_task" || outcome == "before_task" || outcome == "operator_before_proof"
			if invalid {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("invalid round accepted: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			// A reconstructed Postgres adapter must recover proof from durable storage.
			if pg, ok := store.(*PgStore); ok {
				store = NewPgStore(pg.pool.Pool)
				gate = store.(ProjectSafeReleaseStore)
				recorder = store.(ProjectMigrationBaselineStore)
			}
			if !invalid {
				if outcome == "healthy" {
					older := round
					older.Evidence = nil
					replay, err := recorder.RecordProjectMigrationBaseline(ctx, a.ID, attempt, older)
					if err != nil || replay.ReleaseCommand.BaselineVerification.Status != "healthy" {
						t.Fatalf("overlapping unknown round replaced healthy proof: %+v %v", replay, err)
					}
				}
				replay, err := recorder.RecordProjectMigrationBaseline(ctx, a.ID, attempt, round)
				if err != nil || replay.ReleaseCommand.BaselineVerification == nil {
					t.Fatalf("lost-response replay: %+v %v", replay, err)
				}
			}
			if outcome == "operator" {
				if _, err = publisher.PublishProjectReleaseSet(ctx, a.ID, p.ID, "production", 1800, members); err != nil {
					t.Fatal(err)
				}
			}
			progress, err = gate.ReconcileSafeProjectSubmission(ctx, a.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			expected := "building"
			if outcome == "healthy" {
				expected = "verifying"
			}
			if outcome == "failed" {
				expected = "failed"
			}
			if outcome == "operator" || outcome == "operator_before_proof" {
				expected = "conflicted"
			}
			if progress.Status != expected {
				t.Fatalf("status=%s want %s (%s)", progress.Status, expected, progress.Message)
			}
			active, err := reader.ActiveProjectReleaseSet(ctx, a.ID, p.ID, "production")
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "healthy" {
				if active.ID == baseline.ID {
					t.Fatal("healthy expansion did not publish")
				}
			} else if outcome != "operator" && outcome != "operator_before_proof" && active.ID != baseline.ID {
				t.Fatal("unsafe expansion published")
			}
			receipt, err := subs.ProjectSubmission(ctx, a.ID, attempt)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(receipt)
			if strings.Contains(string(encoded), "private migration output") {
				t.Fatal("task output leaked")
			}
			again, err := gate.ReconcileSafeProjectSubmission(ctx, a.ID, attempt)
			if err != nil || again.ReleaseID != progress.ReleaseID || again.ReleaseCommand.TaskID != task.ID {
				t.Fatalf("recovery changed identity: %+v %v", again, err)
			}
		})
	}
}

func TestMigrationBaselineEvidenceFreshness(t *testing.T) {
	probe := api.ProjectReleaseProbe{AppID: "app", Slug: "api", Method: "GET", Path: "/database-health"}
	now := time.Now().UTC()
	finished := now.Add(-time.Minute)
	exit := 0
	command := []string{"expand"}
	task := AppTask{ID: "task", AccountID: "account", AppID: "app", DeploymentID: "candidate", Kind: AppTaskKindRelease, Command: command, ArtifactKey: "root", ImageDigest: "digest", DeploymentScope: "production", Status: AppTaskSucceeded, ExitCode: &exit, FinishedAt: &finished}
	r := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{PreviousReleaseID: "baseline", Verification: &api.ProjectReleaseVerification{Probes: []api.ProjectReleaseProbe{probe}}, ReleaseCommand: &api.ProjectReleaseCommand{AppID: "app", Slug: "api", Compatibility: "backward-compatible", CommandHash: api.ProjectReleaseCommandHash(command, false), VerifyBaseline: true}}}, Builds: []api.AppliedBuild{{AppID: "app", DeploymentID: "candidate"}}}}
	deps := map[string]Deployment{"candidate": {AppID: "app", ReleaseCommand: command, RootfsKey: "root", ImageDigest: "digest", Scope: "production"}}
	for _, tc := range []struct {
		name   string
		age    time.Duration
		status string
	}{{"fresh", time.Second, ""}, {"stale", api.ProjectMigrationBaselineEvidenceMaxAge + time.Second, "building"}, {"future", -time.Minute, "building"}} {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(-tc.age)
			task.FinishedAt = new(time.Time)
			*task.FinishedAt = at.Add(-time.Second)
			r.Apply.SafeRelease.ReleaseCommand.BaselineVerification = &api.ProjectMigrationBaselineVerification{TaskID: "task", ReleaseID: "baseline", StartedAt: at, CheckedAt: at, Status: "healthy", Evidence: []api.ProjectReleaseProbeEvidence{{Probe: probe, StableRequests: api.RouteHealthProbeRequestsPerMinute}}}
			status, _ := projectReleaseCommandOutcome("account", r, deps, map[string]AppTask{"candidate": task})
			if status != tc.status {
				t.Fatalf("got %s want %s", status, tc.status)
			}
		})
	}
}
