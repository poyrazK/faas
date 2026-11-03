package reconcile

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261029090000: a Compose service renamed with x-gregale-renamed-from
// updates its previous name's app in place, keeping the app (and so its
// values, secrets and domains) and its slug, instead of removing it and
// creating a new one.
func TestReconcile_RenamedServiceKeepsItsApp(t *testing.T) {
	store := newFakeStore()
	_, proj := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	ledger := seedApp(t, store, proj, "", "ledger", "")
	seedApp(t, store, proj, "", "web", "")
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "accounts", RenamedFrom: "ledger", Class: reposcan.ClassHTTP, Source: "compose.yaml: accounts", Tier: reposcan.TierCompose},
		{Name: "web", Class: reposcan.ClassHTTP, Source: "compose.yaml: web", Tier: reposcan.TierCompose},
	}}

	plan, err := freshService(store, newFakeAuditor(store)).Plan(context.Background(), proj, scan, "sha-plan", "main", nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Added) != 0 || len(plan.Removed) != 0 || len(plan.Changed) != 1 {
		t.Fatalf("plan added=%d changed=%d removed=%d; want one update", len(plan.Added), len(plan.Changed), len(plan.Removed))
	}

	out, err := freshService(store, newFakeAuditor(store)).Reconcile(context.Background(), proj, scan, "sha-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Added) != 0 || len(out.Removed) != 0 || len(out.Changed) != 1 || out.Changed[0].ID != ledger.ID {
		t.Fatalf("apply added=%d changed=%+v removed=%d; want ledger's app updated in place", len(out.Added), out.Changed, len(out.Removed))
	}
	renamed, err := store.AppByID(context.Background(), ledger.ID)
	if err != nil || renamed.WorkloadName != "accounts" || renamed.Slug != ledger.Slug {
		t.Fatalf("renamed app = %+v, %v; want workload accounts with slug %s", renamed, err, ledger.Slug)
	}
	slugs := PreserveAppSlugs(scan.Workloads, []state.App{ledger}, proj.ID)
	if slugs[0].AppSlug != ledger.Slug {
		t.Fatalf("accounts slug = %q, want ledger's %q", slugs[0].AppSlug, ledger.Slug)
	}
}

// A hint whose previous name has no app, or whose new name already has one,
// changes nothing: the first deploy creates the service, and an existing
// app under the new name is kept while the previous one is removed as
// before.
func TestRenamedWorkloadsAppliesOnlyToAnUnclaimedRename(t *testing.T) {
	billing := state.App{ID: "billing-id", WorkloadName: "billing"}
	payments := state.App{ID: "payments-id", WorkloadName: "payments"}
	workloads := []reposcan.Workload{{Name: "payments", RenamedFrom: "billing"}}
	if got := RenamedWorkloads(workloads, []state.App{billing}); got["billing"] != "payments" {
		t.Fatalf("rename = %v; want billing -> payments", got)
	}
	if got := RenamedWorkloads(workloads, nil); len(got) != 0 {
		t.Fatalf("first deploy rename = %v; want none", got)
	}
	if got := RenamedWorkloads(workloads, []state.App{billing, payments}); len(got) != 0 {
		t.Fatalf("rename onto an existing app = %v; want none", got)
	}
	deleted := billing
	deleted.Status = state.AppDeleted
	if got := RenamedWorkloads(workloads, []state.App{deleted}); len(got) != 0 {
		t.Fatalf("rename from a deleted app = %v; want none", got)
	}
}
