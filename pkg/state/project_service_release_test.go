package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMappedProjectServiceReleaseRequiresExactDependencyPins(t *testing.T) {
	project := state.Project{ID: "project", AccountID: "account", Slug: "shop"}
	apps := []state.App{
		{ID: "backend", AccountID: project.AccountID, ProjectID: project.ID, Slug: "shop-backend", WorkloadName: "api", Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, RevisionPinTTLSeconds: 60}},
		{ID: "frontend", AccountID: project.AccountID, ProjectID: project.ID, Slug: "shop-frontend", WorkloadName: "web", Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, RevisionPinTTLSeconds: 60, ServiceBindings: []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_API_URL", Service: "api"}}}},
	}
	active := uuid.NewString()
	manifest := api.CheckedProjectReleaseManifest{Project: project.Slug, Environment: "production", TTLSeconds: 60, ExpectedActiveReleaseID: &active, ExpectedFallbackDeployments: map[string]string{}, Deployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{}, ServiceTargets: map[string]map[string]string{}}
	snapshot := state.ReleaseStageSnapshot{Project: project, Apps: apps, Active: active, Deployments: map[string]state.Deployment{}, Policies: map[string]api.BindingReleasePolicy{}}
	for _, app := range apps {
		id := uuid.NewString()
		manifest.Deployments[app.Slug] = id
		manifest.ExpectedPolicyRevisions[app.Slug] = 1
		snapshot.Deployments[id] = state.Deployment{ID: id, AppID: app.ID, Scope: manifest.Environment, Status: state.DeployLive, Kind: state.DeploymentKindTarball, RootfsKey: "rootfs", ImageDigest: "image"}
		snapshot.Policies[app.ID] = api.BindingReleasePolicy{Mode: "enforce", Revision: 1}
	}
	manifest.ServiceTargets[apps[0].Slug] = map[string]string{}
	manifest.ServiceTargets[apps[1].Slug] = map[string]string{"api": manifest.Deployments[apps[0].Slug]}
	if _, err := state.ValidateReleaseStage(snapshot, manifest); err != nil {
		t.Fatalf("valid mapped graph rejected: %v", err)
	}
	delete(manifest.ServiceTargets[apps[1].Slug], "api")
	if _, err := state.ValidateReleaseStage(snapshot, manifest); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing mapped dependency pin accepted: %v", err)
	}
}
