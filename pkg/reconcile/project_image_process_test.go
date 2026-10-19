// adr: 1337
package reconcile

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectImageProcessReconcile(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	entrypoint := []string{"tini", "--"}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "gateway", Image: "docker.io/library/nginx:1.27", Class: reposcan.ClassHTTP, SourceSHA256: "accepted",
		ImageProcess: &api.ComposeProcess{Entrypoint: &entrypoint, User: "nginx", WorkingDir: "/srv"},
		Ports:        []int{80}, Tier: reposcan.TierCompose, Source: "compose.yaml: gateway",
	}}}
	result, err := service.Reconcile(context.Background(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if !reflect.DeepEqual(app.Manifest.ProjectImageProcess, scan.Workloads[0].ImageProcess) {
		t.Fatalf("process = %+v", app.Manifest.ProjectImageProcess)
	}
	app.Manifest.ProjectSourceSHA256 = "accepted"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	if result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil || len(result.Changed) != 0 {
		t.Fatalf("unchanged reapply = %+v, %v", result, err)
	}
	scan.Workloads[0].ImageProcess = &api.ComposeProcess{User: "1000"}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Manifest.ProjectImageProcess.User != "1000" ||
		result.Changed[0].Manifest.ProjectImageProcess.Entrypoint != nil {
		t.Fatalf("process-only change = %+v, %v", result, err)
	}
	scan.Workloads[0].Image = ""
	scan.Workloads[0].Dockerfile = "Dockerfile"
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Manifest.ProjectImageProcess != nil {
		t.Fatalf("source transition kept the image process: %+v, %v", result, err)
	}
}

func TestProjectImageProcessAdmission(t *testing.T) {
	limits, _ := api.LimitsFor(api.PlanFree)
	beyond := limits.DefaultStopGracePeriodS + 1
	workloads := []reposcan.Workload{{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
		ImageProcess: &api.ComposeProcess{StopGracePeriodS: &beyond}}}
	reasons := ProjectImageLifecycleAdmissionReasons(api.PlanFree, workloads)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "stop_grace_period") {
		t.Fatalf("Free admission = %v; want the plan grace cap", reasons)
	}
	if reasons := ProjectImageLifecycleAdmissionReasons(api.PlanScale, workloads); len(reasons) != 0 {
		t.Fatalf("Scale admission = %v", reasons)
	}
	workloads[0].ImageProcess = &api.ComposeProcess{WorkingDir: "relative"}
	reasons = WorkloadAdmissionReasons(workloads, nil, "")
	if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, "; "), "invalid image process") {
		t.Fatalf("admission = %v; want the invalid process", reasons)
	}
}
