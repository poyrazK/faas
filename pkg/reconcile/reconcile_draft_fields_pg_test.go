//go:build !no_pg

package reconcile_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Every field a project draft sets survives the Postgres insert. The
// in-memory store keeps whole structs, so a column the SQL insert left out
// passed every reconcile test: unpublished Compose services were created
// public (#5934).
func TestPgReconcileStoresEveryDraftField(t *testing.T) {
	store, svc, _, ctx := pgReconcileStore(t)
	acct, proj := seedAccountProject(t, store, state.ProjectScanSourceCompose)
	replicas := 2
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "api", Source: "compose.yaml: api", Tier: reposcan.TierCompose, Class: reposcan.ClassHTTP,
			Unpublished: true, Dockerfile: "Dockerfile.api",
			Resources: &api.ComposeResources{MemoryMB: 384, CPUMillicores: 500, Replicas: &replicas}},
		{Name: "web", Source: "compose.yaml: web", Tier: reposcan.TierCompose, Class: reposcan.ClassHTTP,
			Ports: []int{3000}, DependsOn: []string{"api"}},
	}}
	if _, err := svc.Reconcile(ctx, proj, scan, "", "", nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	apps, err := store.AppsForProject(ctx, acct.ID, proj.ID)
	if err != nil || len(apps) != len(scan.Workloads) {
		t.Fatalf("apps = %+v, err %v", apps, err)
	}
	for _, w := range scan.Workloads {
		draft := reconcile.DraftAppForScan(proj, scan, w, acct.Plan)
		for _, stored := range apps {
			if stored.WorkloadName == w.Name {
				assertDraftFieldsStored(t, w.Name, draft, stored)
			}
		}
	}
}

// assertDraftFieldsStored compares every exported field the draft set with
// the stored row. The manifest compares as JSON, the form it is stored in;
// the slug is qualified with the project's when the apply allocates it.
func assertDraftFieldsStored(t *testing.T, workload string, draft, stored state.App) {
	t.Helper()
	want, got := reflect.ValueOf(draft), reflect.ValueOf(stored)
	for i := range want.NumField() {
		field := want.Type().Field(i)
		if !field.IsExported() || want.Field(i).IsZero() || field.Name == "Slug" {
			continue
		}
		w, g := want.Field(i).Interface(), got.Field(i).Interface()
		if field.Name == "Manifest" {
			wj, _ := json.Marshal(w)
			gj, _ := json.Marshal(g)
			w, g = string(wj), string(gj)
		}
		if !reflect.DeepEqual(w, g) {
			t.Errorf("%s: %s drafted %v, stored %v", workload, field.Name, w, g)
		}
	}
}
