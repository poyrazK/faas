package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// deletedMemberLookup is the alias read under test: a store, or its pinned
// service-policy view.
type deletedMemberLookup interface {
	ProjectAppByWorkload(context.Context, string, string, string) (state.App, error)
	AppBySlug(context.Context, string) (state.App, error)
}

// A project member keeps its repository service name while it is deleted and
// can still be restored. A sibling's binding to that name was written for the
// member (ADR-20261022120205 gave it a prefixed slug because another app held
// the bare one), so the name must not fall back to that other app's slug in
// another project. See ADR-266 for the project boundary.
func deletedProjectMemberContract(t *testing.T, store state.Store, lookup func(func(deletedMemberLookup))) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "deleted-member@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project := func(slug string) state.Project {
		t.Helper()
		p, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	shop, blog := project("shop"), project("blog")
	create := func(projectID, slug, workload string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: projectID, Slug: slug, WorkloadName: workload,
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	blogSearch := create(blog.ID, "search", "search")
	shopSearch := create(shop.ID, "shop-search", "search")
	shopCaller := state.App{AccountID: account.ID, ProjectID: shop.ID}
	resolve := func(caller state.App) (state.App, error) {
		var app state.App
		var err error
		lookup(func(reader deletedMemberLookup) { app, err = state.AppByServiceLookup(ctx, reader, caller, "search") })
		return app, err
	}
	if app, err := resolve(shopCaller); err != nil || app.ID != shopSearch.ID {
		t.Fatalf("shop's search = %q, %v, want %s", app.Slug, err, shopSearch.Slug)
	}
	if err := store.DeleteApp(ctx, shopSearch.ID); err != nil {
		t.Fatal(err)
	}
	app, err := resolve(shopCaller)
	if !errors.Is(err, state.ErrProjectServiceDeleted) || !errors.Is(err, state.ErrNotFound) || app.ID != "" {
		t.Fatalf("shop's deleted search = %q, %v, want ErrProjectServiceDeleted and never blog's search", app.Slug, err)
	}
	if app, err := state.AppByServiceName(ctx, store, shopCaller, "search"); !errors.Is(err, state.ErrProjectServiceDeleted) || app.ID != "" {
		t.Fatalf("inventory lookup of shop's deleted search = %q, %v, want ErrProjectServiceDeleted", app.Slug, err)
	}
	// Outside shop, the name still means blog's app.
	for _, caller := range []state.App{{AccountID: account.ID, ProjectID: blog.ID}, {AccountID: account.ID}} {
		if app, err := resolve(caller); err != nil || app.ID != blogSearch.ID {
			t.Fatalf("search for caller in project %q = %q, %v, want blog's search", caller.ProjectID, app.Slug, err)
		}
	}
	if _, err := store.RestoreApp(ctx, shopSearch.ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if app, err := resolve(shopCaller); err != nil || app.ID != shopSearch.ID {
		t.Fatalf("restored shop's search = %q, %v, want %s", app.Slug, err, shopSearch.Slug)
	}
}

// adr: 20261022120205
func TestMemDeletedProjectMemberKeepsItsServiceName(t *testing.T) {
	store := state.NewMemStore()
	deletedProjectMemberContract(t, store, func(read func(deletedMemberLookup)) { read(store) })
}

// A project workload's bindings name its own project's services
// (ADR-20261030131528). When no member holds a bound name any more, as after
// a purge, the name resolves to nothing rather than to another app's slug; an
// older binding that names a member by its slug still reaches it. Names the
// caller does not bind keep the slug fallback.
func boundProjectServiceContract(t *testing.T, store state.Store, lookup func(func(deletedMemberLookup))) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "bound-service@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project := func(slug string) state.Project {
		t.Helper()
		p, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: slug})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	shop, blog := project("shop"), project("blog")
	create := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: blog.ID, Slug: slug, WorkloadName: slug,
			Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	blogSearch, blogFeed := create("search"), create("feed")
	// An older binding names a shop member by its project-prefixed slug.
	shopCart, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: shop.ID, Slug: "shop-cart", WorkloadName: "cart",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	bound := state.App{AccountID: account.ID, ProjectID: shop.ID, Manifest: state.AppManifest{
		ServiceBindings: []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_SEARCH_URL", Service: "search"}, {Binding: "GREGALE_SERVICE_SHOP_CART_URL", Service: "shop-cart"}}}}
	unbound := state.App{AccountID: account.ID, ProjectID: shop.ID}
	resolve := func(caller state.App, service string) (state.App, error) {
		var app state.App
		var err error
		lookup(func(reader deletedMemberLookup) { app, err = state.AppByServiceLookup(ctx, reader, caller, service) })
		return app, err
	}
	if app, err := resolve(bound, "search"); !errors.Is(err, state.ErrNotFound) || app.ID != "" {
		t.Fatalf("bound search = %q, %v, want not found and never blog's search", app.Slug, err)
	}
	if app, err := state.AppByServiceName(ctx, store, bound, "search"); !errors.Is(err, state.ErrNotFound) || app.ID != "" {
		t.Fatalf("inventory lookup of bound search = %q, %v, want not found", app.Slug, err)
	}
	for _, tc := range []struct {
		caller  state.App
		service string
		want    state.App
	}{{bound, "shop-cart", shopCart}, {bound, "feed", blogFeed}, {unbound, "search", blogSearch}} {
		if app, err := resolve(tc.caller, tc.service); err != nil || app.ID != tc.want.ID {
			t.Fatalf("%s = %q, %v, want %s", tc.service, app.Slug, err, tc.want.Slug)
		}
	}
}

// adr: 20261030131528
func TestMemProjectBindingNamesOnlyItsProject(t *testing.T) {
	store := state.NewMemStore()
	boundProjectServiceContract(t, store, func(read func(deletedMemberLookup)) { read(store) })
}
