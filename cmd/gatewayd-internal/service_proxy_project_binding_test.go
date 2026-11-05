// adr: 20261030131528 — a project workload's bindings name its own project's
// services, so a binding left behind by a purged member reaches nothing.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyProjectBindingNamesOnlyItsProject(t *testing.T) {
	for _, tc := range []struct {
		policy    api.ServiceBindingPolicy
		authorize error
	}{
		{api.ServiceBindingPolicyDeclared, gateway.ErrServiceProxyBindingDenied},
		{api.ServiceBindingPolicyProject, gateway.ErrServiceProxyBindingDenied},
		// The account policy reaches any service of the account, so only the
		// resolver keeps the binding's name inside the project.
		{api.ServiceBindingPolicyAccount, nil},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "project-binding-"+string(tc.policy)+"@local", api.PlanPro)
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
			blogSearch := create(state.App{ProjectID: blog.ID, Slug: "search", WorkloadName: "search"})
			blogFeed := create(state.App{ProjectID: blog.ID, Slug: "feed", WorkloadName: "feed"})
			// shop's search was deleted and purged; shop-api's binding to it
			// stays until the project is applied again.
			shopAPI := create(state.App{ProjectID: shop.ID, Slug: "shop-api", WorkloadName: "api", Manifest: state.AppManifest{
				ServiceBindingPolicy: tc.policy,
				ServiceBindings:      []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_SEARCH_URL", Service: "search"}},
			}})
			resolve, authorize := newServiceProxyResolver(store), newServiceProxyAuthorizer(store)

			if target, found, err := resolve(ctx, shopAPI.ID, "search"); err != nil || found {
				t.Fatalf("resolve(search) = %+v, %v, %v, want not found (blog's search is %s)", target, found, err, blogSearch.ID)
			}
			if _, err := authorize(ctx, shopAPI.ID, blogSearch.ID); !errors.Is(err, tc.authorize) {
				t.Fatalf("authorize(shop-api -> blog's search) = %v, want %v", err, tc.authorize)
			}
			// A name shop-api does not bind still reaches an app by its slug.
			if target, found, err := resolve(ctx, shopAPI.ID, "feed"); err != nil || !found || target.AppID != blogFeed.ID {
				t.Fatalf("resolve(feed) = %+v, %v, %v, want blog's feed", target, found, err)
			}
		})
	}
}
