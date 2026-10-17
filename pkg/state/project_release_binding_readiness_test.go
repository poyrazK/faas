package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedReleaseServiceReadinessMem(t *testing.T) {
	checkedReleaseServiceReadiness(t, state.NewMemStore(), state.DefaultLocalNodeName)
}
func TestCheckedReleaseServiceReadinessPG(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	checkedReleaseServiceReadiness(t, store, resolveDefaultLocal(t, ctx, store))
}

func checkedReleaseServiceReadiness(t *testing.T, store state.Store, node string) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@ready-graph.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "ready-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "ready-service", WorkloadName: "ready-service", Type: state.AppTypeApp, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:service"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/ready", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}
	first, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Desired: 2, Max: 2}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, account.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	write := func() error {
		revision, err := store.(state.BindingPromotionStore).ReadBindingPromotionRevision(ctx, account.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		fence := state.BindingPromotionFence{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Scope: "production", Revision: revision, PolicyRevision: 1, MaxVerificationAge: api.DefaultBindingVerificationAge, ValidUntil: time.Now().Add(time.Minute)}
		_, err = store.(state.CheckedProjectReleaseStore).ActivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", first.ID, nil, 1800, members, []state.BindingPromotionFence{fence})
		return err
	}
	if err := write(); err == nil {
		t.Fatal("unready service activated")
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "running", 512, node, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	if err := write(); err == nil {
		t.Fatal("partially ready service activated")
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, "running", 512, node, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	if err := write(); err != nil {
		t.Fatalf("ready service blocked: %v", err)
	}
}
