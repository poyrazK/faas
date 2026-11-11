// adr: 20261102192943
package reconcile

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// The release command a Compose completion dependency gives a service is kept
// on its app, so GitHub pushes and single-app deploys run it too; a changed
// command is a change, and dropping the step clears it.
func TestComposeReleaseCommandKeptOnApp(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "api", Dockerfile: "Dockerfile", Class: reposcan.ClassHTTP, SourceSHA256: "accepted",
		Ports: []int{3000}, Tier: reposcan.TierCompose, Source: "compose.yaml: api",
		ReleaseCommand: []string{"npm run migrate"}, ReleaseCommandShell: true, ReleaseService: "migrate",
	}}}
	result, err := service.Reconcile(context.Background(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if !slices.Equal(app.Manifest.ProjectReleaseCommand, []string{"npm run migrate"}) || !app.Manifest.ProjectReleaseCommandShell {
		t.Fatalf("release command = %v shell=%v", app.Manifest.ProjectReleaseCommand, app.Manifest.ProjectReleaseCommandShell)
	}
	app.Manifest.ProjectSourceSHA256 = "accepted"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	if result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil || len(result.Changed) != 0 {
		t.Fatalf("unchanged reapply = %+v, %v", result, err)
	}
	scan.Workloads[0].ReleaseCommand = []string{"npm", "run", "migrate:all"}
	scan.Workloads[0].ReleaseCommandShell = false
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || !slices.Equal(result.Changed[0].Manifest.ProjectReleaseCommand, []string{"npm", "run", "migrate:all"}) || result.Changed[0].Manifest.ProjectReleaseCommandShell {
		t.Fatalf("changed release command = %+v, %v", result, err)
	}
	scan.Workloads[0].ReleaseCommand, scan.Workloads[0].ReleaseService = nil, ""
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Manifest.ProjectReleaseCommand != nil {
		t.Fatalf("removed release command kept: %+v, %v", result, err)
	}
}

// A Procfile release: stays with the source, as before: it is not stored on
// the app and is never a manifest change.
func TestProcfileReleaseCommandNotKeptOnApp(t *testing.T) {
	w := reposcan.Workload{Name: "web", Dockerfile: "Dockerfile", Class: reposcan.ClassHTTP,
		ReleaseCommand: []string{"npm run db:migrate"}, ReleaseCommandShell: true}
	app := state.App{WorkloadName: "web", WorkloadClass: workloadClassFromScan(w)}
	applyProjectImage(&app.Manifest, w)
	if app.Manifest.ProjectReleaseCommand != nil {
		t.Fatalf("procfile release stored: %v", app.Manifest.ProjectReleaseCommand)
	}
	if changed := diffFieldsChanged(app, w, resolveStartCommand(w)); slices.Contains(changed, "release_command") {
		t.Fatalf("procfile release reported as a change: %v", changed)
	}
}
