package bindings

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectServiceTargetsPinMappedAllocations(t *testing.T) {
	caller := state.App{AccountID: "account", ProjectID: "project", Manifest: state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Service: "api"}}}}
	apps := []state.App{{AccountID: caller.AccountID, ProjectID: caller.ProjectID, WorkloadName: "api", Slug: "shop-backend"}}
	selected := ProjectServiceTargets(caller, map[string]string{"shop-backend": "reviewed", "api": "unrelated"}, apps)
	if selected["api"] != "reviewed" {
		t.Fatalf("lost exact reviewed target: %v", selected)
	}
	apps = append(apps, state.App{AccountID: caller.AccountID, ProjectID: caller.ProjectID, WorkloadName: "API", Slug: "ambiguous"})
	if ProjectServiceTargets(caller, map[string]string{"shop-backend": "reviewed"}, apps) != nil {
		t.Fatal("ambiguous graph was accepted")
	}
}
