// adr: 20261022160000
package reconcile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuiltServiceProcessReconcile(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	grace := 20
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "storefront", RootDir: "storefront", Dockerfile: "Dockerfile", Class: reposcan.ClassHTTP, SourceSHA256: "accepted",
		Command:      []string{"tini", "--", "node", "server.js"},
		ImageProcess: &api.ComposeProcess{User: "node", WorkingDir: "/srv/app", StopSignal: "SIGINT", StopGracePeriodS: &grace},
		Ports:        []int{3000}, Tier: reposcan.TierCompose, Source: "compose.yaml: storefront",
	}}}
	result, err := service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if !reflect.DeepEqual(app.Manifest.ProjectImageProcess, scan.Workloads[0].ImageProcess) || app.StartCommand != "tini -- node server.js" {
		t.Fatalf("built service: process=%+v start=%q", app.Manifest.ProjectImageProcess, app.StartCommand)
	}
	app.Manifest.ProjectSourceSHA256 = "accepted"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	scan.Workloads[0].ImageProcess = &api.ComposeProcess{User: "1000"}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 || result.Changed[0].Manifest.ProjectImageProcess.User != "1000" {
		t.Fatalf("process-only change = %+v, %v", result, err)
	}
}

func TestBuiltServiceProcessPlanCap(t *testing.T) {
	limits, _ := api.LimitsFor(api.PlanFree)
	beyond := limits.DefaultStopGracePeriodS + 1
	built := []reposcan.Workload{{Name: "storefront", Class: reposcan.ClassHTTP, Dockerfile: "Dockerfile",
		ImageProcess: &api.ComposeProcess{StopGracePeriodS: &beyond}}}
	reasons := ProjectImageLifecycleAdmissionReasons(api.PlanFree, built)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "stop_grace_period") {
		t.Fatalf("Free admission = %v; want the plan grace cap on a built service", reasons)
	}
	if reasons := ProjectImageLifecycleAdmissionReasons(api.PlanScale, built); len(reasons) != 0 {
		t.Fatalf("Scale admission = %v", reasons)
	}
}
