package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMappedProjectServiceReleaseRequiresExactDependencyPins(t *testing.T) {
	project := Project{ID: "project", AccountID: "account", Slug: "shop"}
	apps := []App{
		{ID: "backend", AccountID: project.AccountID, ProjectID: project.ID, Slug: "shop-backend", WorkloadName: "api", Status: AppActive, Manifest: AppManifest{ExecutionMode: api.ExecutionModeService, RevisionPinTTLSeconds: 60}},
		{ID: "frontend", AccountID: project.AccountID, ProjectID: project.ID, Slug: "shop-frontend", WorkloadName: "web", Status: AppActive, Manifest: AppManifest{ExecutionMode: api.ExecutionModeService, RevisionPinTTLSeconds: 60, ServiceBindings: []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_API_URL", Service: "api"}}}},
	}
	active := uuid.NewString()
	manifest := api.CheckedProjectReleaseManifest{Project: project.Slug, Environment: "production", TTLSeconds: 60, ExpectedActiveReleaseID: &active, ExpectedFallbackDeployments: map[string]string{}, Deployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{}, ServiceTargets: map[string]map[string]string{}}
	snapshot := releaseStageSnapshot{Project: project, Apps: apps, Active: active, Deployments: map[string]Deployment{}, Policies: map[string]api.BindingReleasePolicy{}}
	for _, app := range apps {
		id := uuid.NewString()
		manifest.Deployments[app.Slug] = id
		manifest.ExpectedPolicyRevisions[app.Slug] = 1
		snapshot.Deployments[id] = Deployment{ID: id, AppID: app.ID, Scope: manifest.Environment, Status: DeployLive, Kind: DeploymentKindTarball, RootfsKey: "rootfs", ImageDigest: "image"}
		snapshot.Policies[app.ID] = api.BindingReleasePolicy{Mode: "enforce", Revision: 1}
	}
	manifest.ServiceTargets[apps[0].Slug] = map[string]string{}
	manifest.ServiceTargets[apps[1].Slug] = map[string]string{"api": manifest.Deployments[apps[0].Slug]}
	if _, err := validateReleaseStage(snapshot, manifest); err != nil {
		t.Fatalf("valid mapped graph rejected: %v", err)
	}
	delete(manifest.ServiceTargets[apps[1].Slug], "api")
	if _, err := validateReleaseStage(snapshot, manifest); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing mapped dependency pin accepted: %v", err)
	}
}
