package reconcile

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A1: a slug prefix names new apps; an existing member keeps its slug.
func TestAllocateAppSlugsAppliesPrefixToNewAppsOnly(t *testing.T) {
	workloads := []reposcan.Workload{{Name: "ui", SlugPrefix: "shop-"}, {Name: "cart", SlugPrefix: "shop-"}}
	existing := []state.App{{Slug: "cart", ProjectID: "p1", WorkloadName: "cart", AccountID: "a1", Status: state.AppActive}}
	lookup := func(context.Context, string) (state.App, error) { return state.App{}, state.ErrNotFound }
	out, err := AllocateAppSlugs(context.Background(), lookup, workloads, existing, "a1", "p1", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range out {
		got[w.Name] = AppSlug(w)
	}
	if got["ui"] != "shop-ui" || got["cart"] != "cart" {
		t.Fatalf("slugs = %v, want ui=shop-ui (new) and cart=cart (existing)", got)
	}
}

// A5 and public auth: declared settings apply to a new app within the plan.
func TestApplyWorkloadCreateSettings(t *testing.T) {
	app := state.App{RequireAuthn: true, PublicAuthMode: api.AppPublicAuthModeBearer}
	applyWorkloadCreateSettings(&app, reposcan.Workload{StartupDeadlineS: 105, PublicAuthMode: api.AppPublicAuthModeOpen}, api.PlanPro)
	if want := api.MustLimitsFor(api.PlanPro).MaxStartupDeadlineS; app.Manifest.StartupDeadlineS != want {
		t.Fatalf("StartupDeadlineS = %d, want the Pro cap %d", app.Manifest.StartupDeadlineS, want)
	}
	if app.RequireAuthn || app.PublicAuthMode != api.AppPublicAuthModeOpen {
		t.Fatalf("auth = %v/%q, want open", app.RequireAuthn, app.PublicAuthMode)
	}

	free := state.App{}
	applyWorkloadCreateSettings(&free, reposcan.Workload{StartupDeadlineS: 90, PublicAuthMode: api.AppPublicAuthModeBearer}, api.PlanFree)
	if free.Manifest.StartupDeadlineS != 0 || free.RequireAuthn {
		t.Fatalf("free app = %+v, want the default deadline and no bearer", free)
	}
}
