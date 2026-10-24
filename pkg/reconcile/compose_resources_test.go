// adr: 20261023120000
package reconcile

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func replicas(n int) *int { return &n }

func TestComposeResourcesReconcile(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{{
		Name: "api", Image: "docker.io/library/nginx:1.27", Class: reposcan.ClassHTTP, SourceSHA256: "accepted",
		Resources: &api.ComposeResources{Replicas: replicas(2), MemoryMB: 256, CPUMillicores: 500},
		Ports:     []int{80}, Tier: reposcan.TierCompose, Source: "compose.yaml: api",
	}}}
	result, err := service.Reconcile(context.Background(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	app := result.Added[0]
	if app.RAMMB != 256 || app.CPUMillicores != 500 || app.MaxConcurrency != 2 {
		t.Fatalf("created ram=%d cpu=%d max=%d", app.RAMMB, app.CPUMillicores, app.MaxConcurrency)
	}
	app.Manifest.ProjectSourceSHA256 = "accepted"
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest}); err != nil {
		t.Fatal(err)
	}
	if result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil || len(result.Changed) != 0 {
		t.Fatalf("unchanged reapply = %+v, %v", result, err)
	}
	// Only the declared settings move; the CPU share the app set stays.
	scan.Workloads[0].Resources = &api.ComposeResources{MemoryMB: 512}
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Changed) != 1 {
		t.Fatalf("memory change = %+v, %v", result, err)
	}
	if got := result.Changed[0]; got.RAMMB != 512 || got.CPUMillicores != 500 || got.MaxConcurrency != 2 {
		t.Fatalf("after memory change ram=%d cpu=%d max=%d", got.RAMMB, got.CPUMillicores, got.MaxConcurrency)
	}
	// Removing the declaration keeps the app's settings.
	scan.Workloads[0].Resources = nil
	if result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil || len(result.Changed) != 0 {
		t.Fatalf("undeclared reapply = %+v, %v", result, err)
	}
}

func TestComposeResourcesFollowExecutionMode(t *testing.T) {
	w := reposcan.Workload{Name: "w", Resources: &api.ComposeResources{Replicas: replicas(3)}}
	for _, tc := range []struct {
		mode    string
		check   func(state.App) bool
		changed string
	}{
		{"", func(a state.App) bool { return a.MaxConcurrency == 3 && a.Manifest.WorkerReplicas == nil }, "max_concurrency"},
		{api.ExecutionModeWorker, func(a state.App) bool {
			return a.MaxConcurrency == 1 && a.Manifest.WorkerReplicas != nil && *a.Manifest.WorkerReplicas == state.WorkerScaling{Min: 3, Max: 3}
		}, "worker_replicas"},
		{api.ExecutionModeService, func(a state.App) bool {
			return a.MaxConcurrency == 3 && a.Manifest.ServiceReplicas != nil && *a.Manifest.ServiceReplicas == state.ServiceReplicas{Min: 3, Max: 3, Desired: 3}
		}, "service_replicas"},
		{api.ExecutionModeJob, func(a state.App) bool { return a.MaxConcurrency == 1 }, ""},
	} {
		t.Run("mode "+tc.mode, func(t *testing.T) {
			app := state.App{MaxConcurrency: 1, Manifest: state.AppManifest{ExecutionMode: tc.mode}}
			changed := composeResourcesChanged(app, w)
			if tc.changed != "" && !slices.Contains(changed, tc.changed) || tc.changed == "" && len(changed) != 0 {
				t.Fatalf("changed = %v, want %q", changed, tc.changed)
			}
			if !applyComposeResources(&app, w) || !tc.check(app) {
				t.Fatalf("applied = %+v", app)
			}
			if again := composeResourcesChanged(app, w); len(again) != 0 {
				t.Fatalf("applied app still differs: %v", again)
			}
		})
	}
}

func TestComposeResourcesAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan api.Plan
		w    reposcan.Workload
		want string
	}{
		{"within plan", api.PlanPro, reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
			Resources: &api.ComposeResources{Replicas: replicas(5), MemoryMB: 1024, CPUMillicores: 1000}}, ""},
		{"memory beyond plan", api.PlanHobby, reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
			Resources: &api.ComposeResources{MemoryMB: 1024}}, "memory 1024 MB exceeds the hobby plan's 512 MB per app"},
		{"replicas beyond concurrency", api.PlanHobby, reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
			Resources: &api.ComposeResources{Replicas: replicas(3)}}, "deploy.replicas 3 exceeds the hobby plan's 2 concurrent instances"},
		{"worker replicas beyond plan", api.PlanPro, reposcan.Workload{Name: "jobs", Class: reposcan.ClassWorker, Image: "example.com/worker:v1",
			Resources: &api.ComposeResources{Replicas: replicas(4)}}, "deploy.replicas 4 exceeds the pro plan's 3 worker replicas"},
		{"invalid shape", api.PlanPro, reposcan.Workload{Name: "web", Class: reposcan.ClassHTTP, Image: "example.com/web:v1",
			Resources: &api.ComposeResources{CPUMillicores: 300}}, "one of 250, 500, 1000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reasons := strings.Join(ProjectImageLifecycleAdmissionReasons(tc.plan, []reposcan.Workload{tc.w}), "; ")
			if tc.want == "" && reasons != "" || !strings.Contains(reasons, tc.want) {
				t.Fatalf("reasons = %q, want %q", reasons, tc.want)
			}
		})
	}
}
