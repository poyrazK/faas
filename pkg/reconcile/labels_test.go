package reconcile

import (
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/labels"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app reconcile creates carries its workload's labels; a later reconcile
// leaves the labels of an existing app to its owner (ADR-20261031100025 §7).
func TestReconcileLabelsNewApps(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	service := NewService(store, newFakeAuditor(store), nil)
	workload := func(name string, l map[string]string) reposcan.Workload {
		return reposcan.Workload{Name: name, Image: "docker.io/library/nginx:1.27", Class: reposcan.ClassHTTP,
			Ports: []int{80}, Tier: reposcan.TierCompose, Source: "k8s.yaml: " + name, Labels: l}
	}
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		workload("api", map[string]string{"team": "billing"}),
		workload("web", nil),
	}}
	result, err := service.Reconcile(t.Context(), project, scan, "", "main", nil)
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("create = %+v, %v", result, err)
	}
	ids := map[string]string{}
	for _, app := range result.Added {
		ids[app.WorkloadName] = app.ID
	}
	if got, err := store.AppLabels(t.Context(), ids["api"]); err != nil || !maps.Equal(got, map[string]string{"team": "billing"}) {
		t.Fatalf("api labels = %v, %v", got, err)
	}
	if got, err := store.AppLabels(t.Context(), ids["web"]); err != nil || len(got) != 0 {
		t.Fatalf("web labels = %v, %v; want none", got, err)
	}

	if _, err := store.UpdateAppLabels(t.Context(), ids["api"], labels.Change{Remove: []string{"team"}, Set: map[string]string{"owner": "ops"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(t.Context(), project, scan, "", "main", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := store.AppLabels(t.Context(), ids["api"]); err != nil || !maps.Equal(got, map[string]string{"owner": "ops"}) {
		t.Fatalf("existing app's labels after a reconcile = %v, %v; want the owner's", got, err)
	}
}
