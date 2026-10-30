// adr: 20261022120205 — a deleted project member keeps its service name, so a
// sibling's stale binding never reaches the app in another project that holds
// the name as a slug.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyDeletedProjectMemberKeepsItsName(t *testing.T) {
	for _, policy := range []api.ServiceBindingPolicy{api.ServiceBindingPolicyDeclared, api.ServiceBindingPolicyProject} {
		t.Run(string(policy), func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "deleted-member-"+string(policy)+"@local", api.PlanPro)
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
			create := func(app state.App) state.App {
				t.Helper()
				app.AccountID, app.Type, app.RAMMB, app.Status = account.ID, state.AppTypeApp, 128, state.AppActive
				created, err := store.CreateApp(ctx, app)
				if err != nil {
					t.Fatalf("CreateApp(%s): %v", app.Slug, err)
				}
				return created
			}
			// blog's search holds the bare slug, so shop's got a prefixed one.
			// It may call any service in the account, so only target-side
			// checks can refuse it.
			blogSearch := create(state.App{ProjectID: blog.ID, Slug: "search", WorkloadName: "search",
				Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyAccount}})
			shopSearch := create(state.App{ProjectID: shop.ID, Slug: "shop-search", WorkloadName: "search"})
			shopAPI := create(state.App{ProjectID: shop.ID, Slug: "shop-api", WorkloadName: "api", Manifest: state.AppManifest{
				ServiceBindingPolicy: policy,
				ServiceBindings:      []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_SEARCH_URL", Service: "search"}},
			}})
			// shop's orders admits shop's search by its service name.
			onlySearch := []string{"search"}
			shopOrders := create(state.App{ProjectID: shop.ID, Slug: "shop-orders", WorkloadName: "orders",
				Manifest: state.AppManifest{AllowedServiceCallers: &onlySearch}})
			resolve, authorize := newServiceProxyResolver(store), newServiceProxyAuthorizer(store)

			if target, found, err := resolve(ctx, shopAPI.ID, "search"); err != nil || !found || target.AppID != shopSearch.ID {
				t.Fatalf("resolve(search) = %+v, %v, %v, want shop's search", target, found, err)
			}
			if err := store.DeleteApp(ctx, shopSearch.ID); err != nil {
				t.Fatal(err)
			}
			if target, found, err := resolve(ctx, shopAPI.ID, "search"); err != nil || found {
				t.Fatalf("after shop's search was deleted, resolve(search) = %+v, %v, %v, want not found (blog's search is %s)",
					target, found, err, blogSearch.ID)
			}
			// However blog's search is reached, shop's binding to its own search
			// does not declare it, and shop's caller list does not admit it.
			for _, tc := range []struct {
				name           string
				caller, target state.App
				want           error
			}{
				{"shop's binding does not declare blog's search", shopAPI, blogSearch, gateway.ErrServiceProxyBindingDenied},
				{"shop's caller list does not admit blog's search", blogSearch, shopOrders, gateway.ErrServiceProxyCallerDenied},
			} {
				if _, err := authorize(ctx, tc.caller.ID, tc.target.ID); !errors.Is(err, tc.want) {
					t.Errorf("%s: authorize(%s -> %s) = %v, want %v", tc.name, tc.caller.Slug, tc.target.Slug, err, tc.want)
				}
			}
		})
	}
}
