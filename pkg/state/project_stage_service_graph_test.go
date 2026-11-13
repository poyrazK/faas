package state

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStageServiceGraphMemory(t *testing.T) {
	testStageServiceGraphBeforeReleaseSets(t, NewMemStore())
}

// Hunt 34 H34-33: "deploy --project --env staging" pins each stage deployment
// to its workload settings, including the declared service bindings, but
// publishes no release set. Treating that caller as graph-routed refused every
// staging service call with 403. Until the environment publishes a release
// set, the call is unmanaged and routes in the caller's environment lane
// (ADR-20261027120000); afterwards it stays graph-routed and fail-closed.
func testStageServiceGraphBeforeReleaseSets(t *testing.T, store Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "stage-graph@example.test", api.PlanPro)
	requirePromotionSnapshot(t, err)
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "stage-graph"})
	requirePromotionSnapshot(t, err)
	_, err = store.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	requirePromotionSnapshot(t, err)
	apps := map[string]App{}
	deployments := map[string]Deployment{}
	deployStage := func(name string) Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, Deployment{AppID: apps[name].ID, Scope: "staging", Kind: DeploymentKindTarball, SourceSHA256: strings.Repeat("b", 64)})
		requirePromotionSnapshot(t, err)
		requirePromotionSnapshot(t, store.SetDeploymentRootfs(ctx, d.ID, "/artifacts/stage-"+name, "/artifacts/stage-"+name, 4096))
		requirePromotionSnapshot(t, store.MarkDeploymentLive(ctx, d.ID))
		return d
	}
	for _, name := range []string{"api", "web"} {
		manifest := AppManifest{RevisionPinTTLSeconds: 3600}
		if name == "web" {
			manifest.ServiceBindings = api.ServiceBindingsForTargets([]string{"api"})
		} else {
			manifest.AllowedServiceCallers = &[]string{"web"}
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "stage-graph-" + name, WorkloadName: name, WorkloadClass: WorkloadClassHTTP, Type: AppTypeApp, Manifest: manifest})
		requirePromotionSnapshot(t, err)
		settings, err := WorkloadSettingsFromApp(app)
		requirePromotionSnapshot(t, err)
		_, err = store.(ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
		requirePromotionSnapshot(t, err)
		apps[name] = app
		deployments[name] = deployStage(name)
	}
	graphs := store.(ProjectServiceGraphStore)

	_, managed, found, err := graphs.ResolveProjectServiceBinding(ctx, apps["web"].ID, deployments["web"].ID, "api")
	if err != nil || managed || found {
		t.Fatalf("stage caller without a release set: managed=%t found=%t err=%v; want the environment lane", managed, found, err)
	}

	release, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 1800,
		[]ProjectReleaseMember{{apps["api"].ID, deployments["api"].ID}, {apps["web"].ID, deployments["web"].ID}})
	requirePromotionSnapshot(t, err)
	route, managed, found, err := graphs.ResolveProjectServiceBinding(ctx, apps["web"].ID, deployments["web"].ID, "api")
	if err != nil || !managed || !found || route.TargetDeploymentID != deployments["api"].ID || route.ReleaseSetID != release.ID {
		t.Fatalf("published stage graph: %+v managed=%t found=%t err=%v", route, managed, found, err)
	}

	// A later stage deploy outside the published graph stays fail-closed.
	outside := deployStage("web")
	_, managed, found, _ = graphs.ResolveProjectServiceBinding(ctx, apps["web"].ID, outside.ID, "api")
	if !managed || found {
		t.Fatalf("deployment outside the stage's release graph: managed=%t found=%t; want refused", managed, found)
	}

	// Deactivating the stage's release sets returns later stage deploys to the
	// environment lane; a member of the deactivated set keeps its graph while
	// the set is retained for rollback (ADR-20261019120024 amendment 2026-11-13).
	fallback := []ProjectReleaseMember{{apps["api"].ID, deployments["api"].ID}, {apps["web"].ID, outside.ID}}
	if err := store.(ProjectReleaseSetPromotionStore).DeactivateProjectReleaseSetIfActive(ctx, account.ID, project.ID, "staging", release.ID, fallback); err != nil {
		t.Fatalf("deactivate stage release set: %v", err)
	}
	_, managed, found, err = graphs.ResolveProjectServiceBinding(ctx, apps["web"].ID, outside.ID, "api")
	if err != nil || managed || found {
		t.Fatalf("stage deploy after deactivation: managed=%t found=%t err=%v; want the environment lane", managed, found, err)
	}
	route, managed, found, err = graphs.ResolveProjectServiceBinding(ctx, apps["web"].ID, deployments["web"].ID, "api")
	if err != nil || !managed || !found || route.ReleaseSetID != release.ID {
		t.Fatalf("member of a retained release set: %+v managed=%t found=%t err=%v; want its graph", route, managed, found, err)
	}
}
