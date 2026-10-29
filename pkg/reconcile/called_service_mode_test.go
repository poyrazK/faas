package reconcile

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261029120000: a Compose service without ports that another service
// calls is an HTTP service. An app an earlier deploy created as a worker
// becomes one on the next deploy: class http and no worker mode, so it parks
// when idle and wakes when called.
func TestReconcile_CalledWorkerBecomesAnHTTPService(t *testing.T) {
	store := newFakeStore()
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	apiApp := seedApp(t, store, proj, "api", "api", "", state.WorkloadClassWorker)
	seedApp(t, store, proj, "web", "web", "")
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "api", RootDir: "api", Class: reposcan.ClassHTTP, Unpublished: true, Source: "compose.yaml: api", Tier: reposcan.TierCompose},
		{Name: "web", RootDir: "web", Class: reposcan.ClassHTTP, DependsOn: []string{"api"}, Source: "compose.yaml: web", Tier: reposcan.TierCompose},
	}}

	if changed := diffFieldsChanged(apiApp, scan.Workloads[0], ""); !slices.Contains(changed, "workload_class") {
		t.Fatalf("fields changed = %v; want workload_class", changed)
	}
	if _, err := freshService(store, newFakeAuditor(store)).Reconcile(context.Background(), proj, scan, "sha-1", "main", nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got, err := store.AppByID(context.Background(), apiApp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkloadClass != state.WorkloadClassHTTP || got.Manifest.ExecutionMode != "" || !got.AcceptsRequestInvocations() {
		t.Fatalf("api class=%s mode=%q; want an http app without worker mode", got.WorkloadClass, got.Manifest.ExecutionMode)
	}
}

func TestReleaseScanWorkerMode(t *testing.T) {
	served := reposcan.Workload{Name: "api", Class: reposcan.ClassHTTP}
	worker := reposcan.Workload{Name: "worker", Class: reposcan.ClassWorker}
	for _, tc := range []struct {
		name     string
		previous state.WorkloadClass
		mode     string
		w        reposcan.Workload
		want     string
	}{
		{"a worker the scan now calls", state.WorkloadClassWorker, api.ExecutionModeWorker, served, ""},
		{"a worker that stays one", state.WorkloadClassWorker, api.ExecutionModeWorker, worker, api.ExecutionModeWorker},
		{"an http app its owner set to worker mode", state.WorkloadClassHTTP, api.ExecutionModeWorker, served, api.ExecutionModeWorker},
		{"a worker its owner set to service mode", state.WorkloadClassWorker, api.ExecutionModeService, served, api.ExecutionModeService},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := state.AppManifest{ExecutionMode: tc.mode}
			releaseScanWorkerMode(&manifest, tc.previous, tc.w)
			if manifest.ExecutionMode != tc.want {
				t.Fatalf("execution_mode = %q, want %q", manifest.ExecutionMode, tc.want)
			}
			app := ApplyScannedWorkloadToApp(state.App{WorkloadClass: tc.previous, Manifest: state.AppManifest{ExecutionMode: tc.mode}}, tc.w, nil)
			if app.Manifest.ExecutionMode != tc.want {
				t.Fatalf("preview execution_mode = %q, want %q", app.Manifest.ExecutionMode, tc.want)
			}
		})
	}
}
