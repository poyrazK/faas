package reconcile

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261029120000 amendment 2: when the scan cannot read a service's
// prebuilt image, an existing serving app keeps its class; a worker stays a
// worker, and a new service starts as one.
func TestReconcile_UnreadImageKeepsTheServingClass(t *testing.T) {
	store := newFakeStore()
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	api := seedApp(t, store, proj, "", "api", "")
	worker := seedApp(t, store, proj, "", "worker", "", state.WorkloadClassWorker)
	unread := func(name string) reposcan.Workload {
		return reposcan.Workload{Name: name, Image: "registry.example.com/private/" + name + ":1", Class: reposcan.ClassWorker,
			ListenerUnknown: true, Unpublished: true, Source: "compose.yaml: " + name, Tier: reposcan.TierCompose}
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{unread("api"), unread("worker"), unread("cache")}}

	if _, err := freshService(store, newFakeAuditor(store)).Reconcile(context.Background(), proj, scan, "sha-1", "main", nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, tc := range []struct {
		id   string
		want state.WorkloadClass
	}{{api.ID, state.WorkloadClassHTTP}, {worker.ID, state.WorkloadClassWorker}} {
		got, err := store.AppByID(context.Background(), tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if got.WorkloadClass != tc.want {
			t.Errorf("%s class = %s, want %s", got.WorkloadName, got.WorkloadClass, tc.want)
		}
	}
	apps, err := store.AppsForProject(context.Background(), proj.AccountID, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := slices.IndexFunc(apps, func(a state.App) bool { return a.WorkloadName == "cache" })
	if created < 0 || apps[created].WorkloadClass != state.WorkloadClassWorker {
		t.Fatalf("new service cache = %+v; want a worker", apps)
	}
}
