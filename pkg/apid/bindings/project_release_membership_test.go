package bindings_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemCheckedReleaseUsesEnvironmentMembership(t *testing.T) {
	testCheckedReleaseUsesEnvironmentMembership(t, state.NewMemStore())
}

func testCheckedReleaseUsesEnvironmentMembership(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "checked-membership@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "checked-membership"})
	if err != nil {
		t.Fatal(err)
	}
	web, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "checked-membership-web", WorkloadName: "web", Type: state.AppTypeApp, RAMMB: 256, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: web.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:checked-membership"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/rootfs/"+dep.ID, "apps/"+dep.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	graphs := store.(state.ProjectReleaseSetStore)
	members := []state.ProjectReleaseMember{{AppID: web.ID, DeploymentID: dep.ID}}
	history, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 300, members)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graphs.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 300, members); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, account.ID, web.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	extra, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "checked-membership-experiment", WorkloadName: "experiment", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", extra.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	catalog := store.(bindings.ProjectReleaseCatalog)
	prepared, problem := bindings.PrepareProjectRelease(ctx, catalog, account.ID, project, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 300, Deployments: map[string]string{web.Slug: uuid.MustParse(dep.ID).String()}})
	if problem != nil {
		t.Fatal(problem)
	}
	apps, err := bindings.ProjectReleaseApps(ctx, catalog, account.ID, project.ID, "production")
	if err != nil || len(apps) != 1 || apps[0].ID != web.ID {
		t.Fatalf("production membership: %+v %v", apps, err)
	}
	if _, problem := bindings.ValidateCheckedProjectRelease(prepared, project.Slug, "production", apps); problem != nil {
		t.Fatal(problem)
	}
	rollback, problem := bindings.PrepareProjectRollback(ctx, store.(bindings.ProjectRollbackCatalog), account.ID, project, "production", api.PrepareProjectRollbackRequest{ReleaseID: history.ID, TTLSeconds: 300})
	if problem != nil || len(rollback.Deployments) != 1 {
		t.Fatalf("rollback: %+v %v", rollback, problem)
	}
	deactivation, problem := bindings.PrepareProjectDeactivation(ctx, catalog, account.ID, project, "production")
	if problem != nil || len(deactivation.Deployments) != 1 {
		t.Fatalf("deactivation: %+v %v", deactivation, problem)
	}
	stage, err := bindings.ProjectReleaseApps(ctx, catalog, account.ID, project.ID, "staging")
	if err != nil || len(stage) != 1 || stage[0].ID != extra.ID {
		t.Fatalf("staging membership: %+v %v", stage, err)
	}
	fences := func() []state.BindingPromotionFence {
		t.Helper()
		revision, err := store.(state.BindingPromotionStore).ReadBindingPromotionRevision(ctx, account.ID, web.ID)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := store.(state.BindingReleasePolicyStore).GetBindingReleasePolicy(ctx, account.ID, web.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		age, err := time.ParseDuration(policy.MaxVerificationAge)
		if err != nil {
			t.Fatal(err)
		}
		return []state.BindingPromotionFence{{AccountID: account.ID, AppID: web.ID, DeploymentID: dep.ID, Scope: "production", Revision: revision, PolicyRevision: policy.Revision, MaxVerificationAge: age, ValidUntil: time.Now().Add(time.Minute)}}
	}
	checked := store.(state.CheckedProjectReleaseStore)
	activated, err := checked.ActivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", *prepared.ExpectedActiveReleaseID, nil, 300, members, fences())
	if err != nil {
		t.Fatalf("checked activation with stage-only member: %v", err)
	}
	recovered, err := checked.RollbackProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", activated.ID, history.ID, 300, members, fences())
	if err != nil {
		t.Fatalf("checked rollback with stage-only member: %v", err)
	}
	retired, err := checked.DeactivateProjectReleaseWithBindings(ctx, account.ID, project.ID, "production", recovered.ID, members, fences())
	if err != nil || retired.Active || retired.Deactivation == nil {
		t.Fatalf("checked deactivation with stage-only member: %+v %v", retired, err)
	}
	// The store's staging snapshot must use the same membership as preparation.
	dark, err := store.CreateDeployment(ctx, state.Deployment{AppID: web.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:checked-dark", TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dark.ID, "/rootfs/"+dark.ID, "apps/"+dark.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, dark.ID); err != nil {
		t.Fatal(err)
	}
	serviceManifest := web.Manifest
	serviceManifest.ExecutionMode = api.ExecutionModeService
	if _, err := store.UpdateApp(ctx, web.ID, state.UpdateAppParams{Manifest: &serviceManifest}); err != nil {
		t.Fatal(err)
	}
	serviceRelease, problem := bindings.PrepareProjectRelease(ctx, catalog, account.ID, project, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 300, Deployments: map[string]string{web.Slug: uuid.MustParse(dark.ID).String()}})
	if problem != nil {
		t.Fatal(problem)
	}
	lease, err := store.(state.ProjectReleaseStagingStore).StageProjectRelease(ctx, account.ID, project.ID, api.StageProjectReleaseRequest{Manifest: serviceRelease, TTLSeconds: 60})
	if err != nil || len(lease.Members) != 1 {
		t.Fatalf("another stage blocked the store snapshot: %+v %v", lease, err)
	}
	// Admitting a new production service must still invalidate the old graph.
	if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", extra.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	if _, problem := bindings.PrepareProjectRelease(ctx, catalog, account.ID, project, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 300, Deployments: prepared.Deployments}); problem == nil {
		t.Fatal("omitted a service admitted to production")
	}
}
