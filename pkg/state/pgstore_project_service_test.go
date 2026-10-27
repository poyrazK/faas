package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectServiceLookupScopesAliasesAndRejectsAmbiguity(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "lookup@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "foreign-lookup@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(owner, slug, name, preview string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: owner, ProjectID: project.ID, Slug: slug, WorkloadName: name, PreviewOfSlug: preview, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	target := create(account.ID, "shop-backend", "api", "")
	create(account.ID, "pr-7-shop-backend", "api", target.Slug)
	caller := state.App{AccountID: account.ID, ProjectID: project.ID}
	resolved, err := state.AppByServiceName(ctx, store, caller, "API")
	if err != nil || resolved.ID != target.ID {
		t.Fatalf("wrong mapped target: %+v %v", resolved, err)
	}
	if _, err := store.ProjectAppByWorkload(ctx, foreign.ID, project.ID, "api"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign lookup returned a project target: %v", err)
	}
	create(account.ID, "shop-case-collision", "API", "")
	if _, err := state.AppByServiceName(ctx, store, caller, "api"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ambiguous alias accepted: %v", err)
	}

	// ADR-20261026170512: a Compose billing_api is called billing-api, in
	// production and from its own PR's preview.
	billing := create(account.ID, "shop-billing", "billing_api", "")
	if resolved, err := state.AppByServiceName(ctx, store, caller, "billing-api"); err != nil || resolved.ID != billing.ID {
		t.Fatalf("billing-api resolved to %+v, %v", resolved, err)
	}
	preview, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-9-shop-billing", WorkloadName: "billing_api",
		PreviewOfSlug: billing.Slug, PreviewPrNumber: 9, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if sibling, err := store.PreviewAppByProjectWorkload(ctx, account.ID, project.ID, 9, "billing-api"); err != nil || sibling.ID != preview.ID {
		t.Fatalf("preview sibling billing-api = %+v, %v", sibling, err)
	}
}
