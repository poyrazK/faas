// adr: 583
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeDoesNotBorrowRunningInstancesAcrossScopes(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, production := seedApp(t, store, api.PlanPro, 256, 5)
	staging, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive,
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:stage", OverridePort: 9090})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	stage, err := engine.Wake(ctx, app.ID, "", "staging", "")
	if err != nil || stage.DeploymentID != staging.ID {
		t.Fatalf("stage wake = %+v, %v", stage, err)
	}
	prod, err := engine.Wake(ctx, app.ID, "", "", "")
	if err != nil || prod.DeploymentID != production.ID || prod.InstanceID == stage.InstanceID || vmm.coldBoots != 2 {
		t.Fatalf("production borrowed running stage: %+v, boots=%d, err=%v", prod, vmm.coldBoots, err)
	}
	stageAgain, err := engine.Wake(ctx, app.ID, "", "staging", "")
	if err != nil || stageAgain.InstanceID != stage.InstanceID || stageAgain.DeploymentID != staging.ID || stageAgain.Port != 9090 || stageAgain.Identity.ImageDigest != "sha256:stage" {
		t.Fatalf("stage fast path lost deployment identity: %+v, %v", stageAgain, err)
	}
	prodAgain, err := engine.Wake(ctx, app.ID, "", "", "")
	if err != nil || prodAgain.InstanceID != prod.InstanceID || prodAgain.Identity.DeploymentID != production.ID || vmm.coldBoots != 2 {
		t.Fatalf("production fast path borrowed stage: %+v, boots=%d, err=%v", prodAgain, vmm.coldBoots, err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, production.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := engine.Wake(ctx, app.ID, "", "", ""); !errors.Is(err, ErrPermanentWake) || got.InstanceID != "" || vmm.coldBoots != 2 {
		t.Fatalf("stage-only app implicitly woke production: %+v, boots=%d, err=%v", got, vmm.coldBoots, err)
	}
}

func TestWakeModeMatchesPinnedStageConfiguration(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, _, _ := seedApp(t, store, api.PlanPro, 256, 5)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stage-mode"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "stage-mode-api", RAMMB: 256})
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
	settings.Manifest.ExecutionMode = api.ExecutionModeWorker
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	instance := state.Instance{AppID: app.ID, DeploymentID: deployment.ID, Mode: string(state.InstanceModeWorker)}
	if !engine.wakeInstanceModeMatchesApp(ctx, app.ID, instance) {
		t.Fatal("running stage mode was checked against production configuration")
	}
	instance.Mode = string(state.InstanceModeNormal)
	if engine.wakeInstanceModeMatchesApp(ctx, app.ID, instance) {
		t.Fatal("wrong stage mode was accepted")
	}
}
