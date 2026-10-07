// adr: 638, 642
package reconcile

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectImageReconcileAndSourceTransition(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "gateway", Image: "docker.io/library/nginx:1.27", ImageHealthcheck: &api.ComposeHealthcheck{Test: []string{"CMD", "/check"}}, Command: []string{"serve", "with spaces"},
		Class: reposcan.ClassHTTP, SourceSHA256: "accepted", Ports: []int{80},
		Tier: reposcan.TierCompose, Source: "compose.yaml: gateway",
	}}}
	result, err := service.Reconcile(context.Background(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if app.Manifest.ProjectImage != scan.Workloads[0].Image || app.Manifest.ProjectImagePort != 80 ||
		!reflect.DeepEqual(app.Manifest.ProjectImageCommand, scan.Workloads[0].Command) {
		t.Fatalf("image metadata = %+v", app.Manifest)
	}
	if !reflect.DeepEqual(app.Manifest.ProjectImageHealthcheck, scan.Workloads[0].ImageHealthcheck) {
		t.Fatalf("healthcheck metadata = %+v", app.Manifest.ProjectImageHealthcheck)
	}
	app.Manifest.ProjectSourceSHA256 = "accepted"
	app.Manifest.Env = map[string]string{"CUSTOM_VALUE": "keep"}
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 0 {
		t.Fatalf("reapply = %+v, %v", result, err)
	}
	scan.Workloads[0].ImageHealthcheck = &api.ComposeHealthcheck{Test: []string{"NONE"}}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Manifest.ProjectImageHealthcheck.Test[0] != "NONE" {
		t.Fatalf("healthcheck-only change = %+v, %v", result, err)
	}
	scan.Workloads[0].Image = "docker.io/library/nginx:1.28"
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].ID != app.ID {
		t.Fatalf("image update = %+v, %v", result, err)
	}
	preview := ApplyScannedWorkloadToApp(result.Changed[0], scan.Workloads[0], nil)
	if preview.Manifest.ProjectImage != scan.Workloads[0].Image {
		t.Fatalf("preview lost image intent: %+v", preview)
	}
	scan.Workloads[0].Image = ""
	scan.Workloads[0].Dockerfile = "Dockerfile"
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 {
		t.Fatalf("source transition = %+v, %v", result, err)
	}
	manifest := result.Changed[0].Manifest
	if manifest.ProjectImage != "" || manifest.ProjectImageCommand != nil || manifest.ProjectImagePort != 0 || manifest.ProjectImageHealthcheck != nil || manifest.BuildDockerfile != "Dockerfile" || manifest.Env["CUSTOM_VALUE"] != "keep" {
		t.Fatalf("source transition metadata = %+v", manifest)
	}
}

func TestProjectImageShellCommandMetadata(t *testing.T) {
	w := reposcan.Workload{Name: "worker", Class: reposcan.ClassWorker, Image: "example.com/worker:v1", Command: []string{"exec /worker"}, CommandShell: true}
	app := workloadToDraftApp(state.Project{}, w, resolveStartCommand(w), api.PlanPro)
	if !reflect.DeepEqual(app.Manifest.ProjectImageCommand, []string{"/bin/sh", "-c", "exec /worker"}) {
		t.Fatalf("command = %v", app.Manifest.ProjectImageCommand)
	}
	if app.Manifest.ExecutionMode != api.ExecutionModeWorker {
		t.Fatalf("background image mode = %q", app.Manifest.ExecutionMode)
	}
}

func TestProjectImageWorkerLifecyclePlanGate(t *testing.T) {
	workloads := []reposcan.Workload{{Name: "worker", Class: reposcan.ClassWorker, Image: "example.com/worker:v1"}}
	if reasons := ProjectImageLifecycleAdmissionReasons(api.PlanFree, workloads); len(reasons) != 1 {
		t.Fatalf("Free worker admission = %v", reasons)
	}
	if reasons := ProjectImageLifecycleAdmissionReasons(api.PlanPro, workloads); len(reasons) != 0 {
		t.Fatalf("Pro worker admission = %v", reasons)
	}
}

func TestProjectImageAdoptionDefaultsExistingWorkerLifecycle(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	app := seedApp(t, store, project, "", "worker", "", state.WorkloadClassWorker)
	service := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{Name: "worker",
		Class: reposcan.ClassWorker, Tier: reposcan.TierCompose, Source: "compose.yaml: worker",
		Image: "example.com/worker:v1"}}}
	result, err := service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].ID != app.ID || result.Changed[0].Manifest.ExecutionMode != api.ExecutionModeWorker {
		t.Fatalf("worker adoption = %+v, %v", result, err)
	}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 0 {
		t.Fatalf("worker reapply = %+v, %v", result, err)
	}
}
