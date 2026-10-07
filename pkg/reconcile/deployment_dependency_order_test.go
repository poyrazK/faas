// adr: 646
package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectDependencyAdmissionOrderingAndFailure(t *testing.T) {
	web := state.App{WorkloadName: "web", Manifest: state.AppManifest{
		ServiceBindings:             []api.AppServiceBinding{{Service: "backend"}},
		ProjectDependencyConditions: map[string]string{"backend": api.ComposeDependencyHealthy},
	}}
	apiApp := state.App{WorkloadName: "backend"}
	ordered, err := OrderProjectDeploymentApps([]state.App{web, apiApp})
	if err != nil || len(ordered) != 2 || ordered[0].WorkloadName != "backend" {
		t.Fatalf("order = %+v, %v", ordered, err)
	}
	selected := ProjectDeploymentSelection(ordered)
	if blocker := ProjectDependencyAdmissionBlocker(web, selected, nil); blocker == "" {
		t.Fatal("failed dependency fell back to an older live release")
	}
	if blocker := ProjectDependencyAdmissionBlocker(web, selected, map[string]bool{"backend": true}); blocker != "" {
		t.Fatal(blocker)
	}
	if blocker := ProjectDependencyAdmissionBlocker(web, ProjectDeploymentSelection([]state.App{web}), nil); blocker != "" {
		t.Fatalf("untouched dependency blocked admission: %s", blocker)
	}
}

func TestProjectDependencyConditionReconcileChanges(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "backend", Image: "docker.io/library/nginx:1.27", Tier: reposcan.TierCompose, Source: "compose.yaml: api"},
		{Name: "web", Image: "docker.io/library/nginx:1.27", Tier: reposcan.TierCompose, Source: "compose.yaml: web", DependsOn: []string{"backend"}, DependsOnConditions: map[string]string{"backend": api.ComposeDependencyHealthy}},
	}}
	result, err := service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("create: %+v, %v", result, err)
	}
	var web state.App
	for _, app := range result.Added {
		if app.WorkloadName == "web" {
			web = app
		}
	}
	if web.Manifest.ProjectDependencyConditions["backend"] != api.ComposeDependencyHealthy {
		t.Fatal("condition lost at reconciliation")
	}
	scan.Workloads[1].DependsOnConditions = nil
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || len(result.Changed[0].Manifest.ProjectDependencyConditions) != 0 {
		t.Fatalf("remove condition: %+v, %v", result, err)
	}
}
