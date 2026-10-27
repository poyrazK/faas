// adr: 20261025134512
package reconcile

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Each command of a multi-command Go module becomes its own app, built from
// the module root with its command recorded in the manifest.
func TestGoModuleCommandsReconcile(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceConvention, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	workload := func(name string) reposcan.Workload {
		return reposcan.Workload{Name: name, RootDir: ".", GoCommand: name, SourceSHA256: "module", Tier: reposcan.TierConvention,
			Source: "convention: cmd/" + name + " (Go module root)"}
	}
	scan := reposcan.Result{Tier: reposcan.TierConvention, Workloads: []reposcan.Workload{workload("api"), workload("worker")}}
	result, err := service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	for _, app := range result.Added {
		if app.RootDir != "." || app.Manifest.BuildGoCommand != app.WorkloadName {
			t.Fatalf("%s: root %q, go command %q", app.WorkloadName, app.RootDir, app.Manifest.BuildGoCommand)
		}
	}

	app := result.Added[0]
	same := workload(app.WorkloadName)
	if slices.Contains(diffFieldsChanged(app, same, ""), "go_command") {
		t.Fatal("an unchanged command is reported as changed")
	}
	same.GoCommand = "other"
	if !slices.Contains(diffFieldsChanged(app, same, ""), "go_command") {
		t.Fatal("a command change is not reported as go_command")
	}
	if got := ApplyScannedWorkloadToApp(state.App{}, workload("worker"), nil); got.Manifest.BuildGoCommand != "worker" {
		t.Fatalf("preview projection go command = %q", got.Manifest.BuildGoCommand)
	}
}
