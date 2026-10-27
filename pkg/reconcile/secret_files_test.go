// adr: 20261023090000
package reconcile

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSecretFilesReconcile(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	files := []api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password", Mode: 0o400}}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "api", Image: "docker.io/library/nginx:1.27", Class: reposcan.ClassHTTP, SourceSHA256: "accepted",
		SecretFiles: files, Ports: []int{80}, Tier: reposcan.TierCompose, Source: "compose.yaml: api",
	}}}
	result, err := service.Reconcile(context.Background(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if !reflect.DeepEqual(app.Manifest.ProjectSecretFiles, files) {
		t.Fatalf("secret files = %+v", app.Manifest.ProjectSecretFiles)
	}
	app.Manifest.ProjectSourceSHA256 = "accepted"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	if result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil || len(result.Changed) != 0 {
		t.Fatalf("unchanged reapply = %+v, %v", result, err)
	}
	// A built service carries them too, and a changed mode is a change.
	scan.Workloads[0].Image = ""
	scan.Workloads[0].Dockerfile = "Dockerfile"
	scan.Workloads[0].SecretFiles = []api.SecretFile{{Secret: "DB_PASSWORD", Name: "db_password"}}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || !reflect.DeepEqual(result.Changed[0].Manifest.ProjectSecretFiles, scan.Workloads[0].SecretFiles) {
		t.Fatalf("built service secret files = %+v, %v", result, err)
	}
	scan.Workloads[0].SecretFiles = nil
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Manifest.ProjectSecretFiles != nil {
		t.Fatalf("removed secret files kept: %+v, %v", result, err)
	}
}

func TestSecretFilesDiff(t *testing.T) {
	files := []api.SecretFile{{Secret: "TOKEN", Name: "token"}}
	w := reposcan.Workload{Name: "api", Image: "docker.io/library/nginx:1.27", Class: reposcan.ClassHTTP, SecretFiles: files}
	app := state.App{WorkloadName: "api", WorkloadClass: workloadClassFromScan(w)}
	applyProjectImage(&app.Manifest, w)
	if changed := diffFieldsChanged(app, w, resolveStartCommand(w)); slices.Contains(changed, "secret_files") {
		t.Fatalf("matching files reported changed: %v", changed)
	}
	w.SecretFiles = []api.SecretFile{{Secret: "TOKEN", Name: "token", Mode: 0o400}}
	if changed := diffFieldsChanged(app, w, resolveStartCommand(w)); !slices.Contains(changed, "secret_files") {
		t.Fatalf("mode change not reported: %v", changed)
	}
}

func TestSecretFilesAdmission(t *testing.T) {
	workloads := []reposcan.Workload{{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
		SecretFiles: []api.SecretFile{{Secret: "TOKEN", Name: "a/b"}}}}
	reasons := WorkloadAdmissionReasons(workloads, nil, "")
	if !strings.Contains(strings.Join(reasons, "; "), "invalid secret files") {
		t.Fatalf("admission = %v; want the invalid secret file", reasons)
	}
}
