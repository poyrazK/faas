// adr: 20261022131852 — a new Compose service without `ports:` is internal.
package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDraftAppVisibilityFollowsComposePublication(t *testing.T) {
	for _, tc := range []struct {
		name string
		w    reposcan.Workload
		want api.AppVisibility
	}{
		{"unpublished compose service", reposcan.Workload{Name: "billing", Unpublished: true}, api.AppVisibilityInternal},
		{"published compose service", reposcan.Workload{Name: "web", Ports: []int{3000}}, ""},
		{"non-compose workload", reposcan.Workload{Name: "app"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workloadToDraftApp(state.Project{}, tc.w, "", api.PlanPro).Visibility; got != tc.want {
				t.Fatalf("Visibility = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReconcileCreatesUnpublishedServiceInternalAndKeepsOwnerChoice(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	svc := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "web", Source: "compose.yaml: web", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose, Ports: []int{3000}, DependsOn: []string{"ledger"}},
		{Name: "ledger", Source: "compose.yaml: ledger", Class: reposcan.ClassWorker, Tier: reposcan.TierCompose, Unpublished: true},
	}}
	created, err := svc.Reconcile(t.Context(), project, scan, "", "", nil)
	if err != nil || len(created.Added) != 2 {
		t.Fatalf("%+v %v", created, err)
	}
	var ledgerID string
	for _, app := range created.Added {
		stored, err := store.AppByID(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := api.AppVisibility("")
		if app.WorkloadName == "ledger" {
			want, ledgerID = api.AppVisibilityInternal, app.ID
		}
		if api.NormalizeAppVisibility(stored.Visibility) != api.NormalizeAppVisibility(want) {
			t.Fatalf("%s visibility = %q, want %q", app.WorkloadName, stored.Visibility, want)
		}
	}

	// The owner publishes the service; a later sync must not hide it again.
	public := api.AppVisibilityPublic
	if _, err := store.UpdateApp(t.Context(), ledgerID, state.UpdateAppParams{Visibility: &public, SetVisibility: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reconcile(t.Context(), project, scan, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if stored, err := store.AppByID(t.Context(), ledgerID); err != nil || api.NormalizeAppVisibility(stored.Visibility) != api.AppVisibilityPublic {
		t.Fatalf("after re-sync: %+v %v, want the owner's public choice", stored.Visibility, err)
	}
}
