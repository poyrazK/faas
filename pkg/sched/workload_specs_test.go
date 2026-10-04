// adr: 568
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSchedulerResolvesDeploymentWorkloadRevision(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "scheduler-workload-spec@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "scheduler-spec"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "scheduler-spec-api",
		RAMMB: 256, CPUMillicores: 250, Status: state.AppActive, StartCommand: "serve production"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.CPUMillicores, settings.StartCommand = 512, 500, "serve staging"
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.StartCommand = 1024, "serve next"
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{store: store}
	resolved, _, _, dep, err := engine.resolveApp(WithScope(ctx, "staging"), app.ID)
	if err != nil || dep.ID != deployment.ID || resolved.RAMMB != 512 || resolved.CPUMillicores != 500 || resolved.StartCommand != "serve staging" {
		t.Fatalf("scheduler lost tested stage revision: %+v, dep=%s, err=%v", resolved, dep.ID, err)
	}
	resolved, _, _, dep, err = engine.resolveApp(ctx, app.ID)
	if err != nil || dep.ID != production.ID || resolved.RAMMB != 256 || resolved.StartCommand != "serve production" {
		t.Fatalf("unscoped scheduler selected staging: %+v, dep=%s, err=%v", resolved, dep.ID, err)
	}
}

func TestParkUsesPinnedEnvironmentResourceShape(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, _, _ := seedApp(t, store, api.PlanPro, 256, 5)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "park-spec"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "park-spec-api", RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB = 512
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	vmm, notifier := &fakeVMM{}, &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")
	instanceID := primeRunPlusFrameworkReady(t, store, vmm, notifier, engine, app.ID, deployment.ID)
	instance, err := store.InstanceByID(ctx, instanceID)
	if err != nil || instance.RAMMB != 512 {
		t.Fatalf("prime lost stage RAM: %+v, %v", instance, err)
	}
	settings.RAMMB = 128
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	if err := engine.Park(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	instance, err = store.InstanceByID(ctx, instanceID)
	if err != nil || instance.State != string(state.StateParked) || vmm.snapshots != 1 || vmm.destroys != 0 || notifier.count(db.NotifySnapshotWritten) != 1 {
		t.Fatalf("park used shared/desired RAM instead of pinned RAM: instance=%+v snapshots=%d destroys=%d err=%v", instance, vmm.snapshots, vmm.destroys, err)
	}
}
