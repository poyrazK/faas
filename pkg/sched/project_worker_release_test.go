package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectWorkerTargetsFollowGraphAcrossCandidateCutoverAndRollback(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "worker-targets@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "worker-targets"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "worker-targets", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:old", Status: state.DeployLive, CreatedAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:new", TrafficPercentExplicit: true, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	assertTarget := func(expected string) {
		t.Helper()
		deps, err := store.LiveDeployments(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		targets, _, err := engine.projectWorkerTargets(ctx, app, deps)
		if err != nil || len(targets) != 1 {
			t.Fatalf("targets=%v %v", targets, err)
		}
		if _, ok := targets[expected]; !ok {
			t.Fatalf("wrong worker generation: %+v expected %s", targets, expected)
		}
	}
	assertTarget(old.ID)
	release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: candidate.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if release.ID == baseline.ID {
		t.Fatal("cutover did not create graph")
	}
	assertTarget(candidate.ID)
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: old.ID}}); err != nil {
		t.Fatal(err)
	}
	assertTarget(old.ID)
}
