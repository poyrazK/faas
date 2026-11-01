package sched

// adr: 20261031193635

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedEnvironmentBranch publishes a live production deployment of one app and
// returns the deployment a branch of production created from it.
func seedEnvironmentBranch(t *testing.T, store *state.MemStore, mode string) (state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "branch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "branched"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web",
		Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.ExecutionMode = mode
	if app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:branched"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/branched.ext4", "layers/branched.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	if _, _, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-1",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, dep
}

// A branch deployment of a request workload goes ready without a VM; its
// first request cold boots.
func TestPrimeEnvironmentBranchRequestActivatesWithoutBoot(t *testing.T) {
	store := state.NewMemStore()
	app, dep := seedEnvironmentBranch(t, store, api.ExecutionModeRequest)
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime(branch): %v", err)
	}
	if vmm.coldBoots != 0 || vmm.restores != 0 || vmm.snapshots != 0 {
		t.Fatalf("branch prime executed VM work: cold=%d restore=%d snapshot=%d", vmm.coldBoots, vmm.restores, vmm.snapshots)
	}
	if instances, err := store.ListInstancesForApp(context.Background(), app.ID); err != nil || len(instances) != 0 {
		t.Fatalf("branch prime instances = %d, %v; want none", len(instances), err)
	}
	var ready struct {
		DeploymentID     string `json:"deployment_id"`
		ExecutionMode    string `json:"execution_mode"`
		InstanceID       string `json:"instance_id"`
		NoSnapshotReason string `json:"no_snapshot_reason"`
	}
	payload := lastNotifyPayload(t, notifier, db.NotifyDeploymentReady)
	if err := json.Unmarshal([]byte(payload), &ready); err != nil {
		t.Fatal(err)
	}
	if ready.DeploymentID != dep.ID || ready.ExecutionMode != api.ExecutionModeRequest || ready.InstanceID != "" ||
		ready.NoSnapshotReason != state.EnvironmentBranchActivationReason {
		t.Fatalf("branch readiness = %s", payload)
	}
}

// A worker runs continuously, so its branch deployment keeps the normal
// prime: one cold boot that stays running.
func TestPrimeEnvironmentBranchWorkerStillBoots(t *testing.T) {
	store := state.NewMemStore()
	app, dep := seedEnvironmentBranch(t, store, api.ExecutionModeWorker)
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")

	if err := engine.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime(branch worker): %v", err)
	}
	if vmm.coldBoots != 1 {
		t.Fatalf("branch worker cold boots = %d; want 1", vmm.coldBoots)
	}
}
