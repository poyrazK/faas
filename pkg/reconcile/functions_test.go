package reconcile

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func functionWorkload(name, root, runtime, handler string) reposcan.Workload {
	return reposcan.Workload{
		Name: name, RootDir: root, Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose,
		Source:     "gregale.yaml: functions." + name,
		DetectedBy: reposcan.Detection{Detector: "functions"},
		Function:   &reposcan.FunctionWorkload{Runtime: runtime, Handler: handler},
	}
}

// A functions project's push creates function apps named <project>-<name>,
// adopts the one `gregale deploy` already made, and keeps a function the
// repository stopped declaring (ADR-20261108160000).
func TestReconcileFunctionsProject(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	cliApp, err := store.CreateApp(context.Background(), state.App{
		AccountID: proj.AccountID, Slug: "demo-digest", Type: state.AppTypeFunction, Runtime: "python312",
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		functionWorkload("api", "", "node22", "src/api.mjs"),
		functionWorkload("digest", "jobs/digest", "python312", "handler.handler"),
	}}
	svc := freshService(store, aud)
	out, err := svc.Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Added) != 2 {
		t.Fatalf("added = %+v, want api created and digest adopted", out.Added)
	}
	bySlug := map[string]state.App{}
	for _, app := range out.Added {
		bySlug[app.Slug] = app
	}
	api, ok := bySlug["demo-api"]
	if !ok || api.Type != state.AppTypeFunction || api.Runtime != "node22" || api.ProjectID != proj.ID || api.WorkloadName != "api" {
		t.Errorf("demo-api = %+v, want a node22 function app in the project", api)
	}
	digest, ok := bySlug["demo-digest"]
	if !ok || digest.ID != cliApp.ID || digest.ProjectID != proj.ID || digest.RootDir != "jobs/digest" || digest.WorkloadName != "digest" {
		t.Errorf("demo-digest = %+v, want the CLI app %s adopted into the project", digest, cliApp.ID)
	}

	// The repository drops digest: its app is kept, not removed.
	scan.Workloads = scan.Workloads[:1]
	out, err = svc.Reconcile(context.Background(), proj, scan, "sha-2", "main", nil)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(out.Removed) != 0 {
		t.Fatalf("removed = %+v, want function apps kept", out.Removed)
	}
	if kept, err := store.AppBySlug(context.Background(), "demo-digest"); err != nil || kept.Status != state.AppActive {
		t.Fatalf("demo-digest after removal from the repository = %+v, %v; want it active", kept, err)
	}
}

// An app holding the slug that is not an adoptable function app still
// refuses the plan.
func TestReconcileFunctionsProjectSlugHeldByAnotherApp(t *testing.T) {
	store := newFakeStore()
	aud := newFakeAuditor(store)
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	if _, err := store.CreateApp(context.Background(), state.App{
		AccountID: proj.AccountID, Slug: "demo-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
	}); err != nil {
		t.Fatal(err)
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{functionWorkload("api", "", "node22", "src/api.mjs")}}
	if _, err := freshService(store, aud).Reconcile(context.Background(), proj, scan, "sha-1", "main", nil); err == nil {
		t.Fatal("a plain app holding demo-api must refuse the plan")
	}
}
