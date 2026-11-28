package reconcile

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Compose service built from source records that its RootDir is its build
// context, so a GitHub build narrows to it as a project deploy does (trial
// 2026-11-28). An image service and a non-Compose workload do not.
func TestReconcileRecordsComposeBuildContext(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	compose := reposcan.Detection{Detector: reposcan.DetectorCompose}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "web", RootDir: "web", Class: reposcan.ClassHTTP, SourceSHA256: "web-1", Ports: []int{8080},
			Tier: reposcan.TierCompose, Source: "compose.yaml: web", DetectedBy: compose},
		{Name: "proxy", Image: "docker.io/library/nginx:1.27", Class: reposcan.ClassHTTP, SourceSHA256: "proxy-1", Ports: []int{80},
			Tier: reposcan.TierCompose, Source: "compose.yaml: proxy", DetectedBy: compose},
	}}
	result, err := service.Reconcile(context.Background(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	got := map[string]bool{}
	for _, app := range result.Added {
		got[app.WorkloadName] = app.Manifest.BuildComposeContext
	}
	if !got["web"] || got["proxy"] {
		t.Fatalf("build_compose_context = %v; want web only", got)
	}

	// An app reconciled before the field existed gets it with its next change.
	web := result.Added[0]
	if web.WorkloadName != "web" {
		web = result.Added[1]
	}
	web.Manifest.BuildComposeContext = false
	if _, err := store.UpdateApp(t.Context(), web.ID, state.UpdateAppParams{Manifest: &web.Manifest}); err != nil {
		t.Fatal(err)
	}
	scan.Workloads[0].SourceSHA256 = "web-2"
	result, err = service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	var changed *state.App
	for i := range result.Changed {
		if result.Changed[i].ID == web.ID {
			changed = &result.Changed[i]
		}
	}
	if changed == nil || !changed.Manifest.BuildComposeContext {
		t.Fatalf("changed web = %+v; want build_compose_context set", changed)
	}

	// A preview of the service carries it too.
	if preview := ApplyScannedWorkloadToApp(state.App{}, scan.Workloads[0], nil); !preview.Manifest.BuildComposeContext {
		t.Fatalf("preview manifest = %+v; want build_compose_context", preview.Manifest)
	}
	if preview := ApplyScannedWorkloadToApp(state.App{}, reposcan.Workload{Name: "api", RootDir: "api"}, nil); preview.Manifest.BuildComposeContext {
		t.Fatal("a non-Compose workload got build_compose_context")
	}
}
