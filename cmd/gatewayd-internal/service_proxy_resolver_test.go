// adr: 212
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyResolverPrefersSamePRPreviewWorkload(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "resolver-preview@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "resolver-preview"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	create := func(app state.App) state.App {
		t.Helper()
		app.AccountID = account.ID
		app.ProjectID = project.ID
		app.RAMMB = 128
		app.Status = state.AppActive
		created, createErr := store.CreateApp(ctx, app)
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", app.Slug, createErr)
		}
		return created
	}
	production := create(state.App{Slug: "billing", WorkloadName: "billing", AppProtocol: api.AppProtocolHTTP1})
	caller := create(state.App{
		Slug: "pr-42-api", WorkloadName: "api", PreviewOfSlug: "api", PreviewPrNumber: 42,
	})
	sibling := create(state.App{
		Slug: "pr-42-billing", WorkloadName: "billing", PreviewOfSlug: production.Slug, PreviewPrNumber: 42,
		AppProtocol: api.AppProtocolGRPC, WebSocketEnabled: true,
	})

	target, ok, err := newServiceProxyResolver(store)(ctx, caller.ID, "billing")
	if err != nil || !ok {
		t.Fatalf("resolve = (%+v, %v, %v), want preview target", target, ok, err)
	}
	if target.AppID != sibling.ID || !target.PreviewScoped {
		t.Fatalf("target = %+v, want preview app %q", target, sibling.ID)
	}
	if target.AppProtocol != api.AppProtocolGRPC || !target.WebSocketEnabled {
		t.Fatalf("target protocol posture = %+v, want sibling grpc + websocket", target)
	}

	// The default project policy is deny. Selecting the preview sibling must
	// still authorize because no production boundary was crossed.
	if _, err := newServiceProxyAuthorizer(store)(ctx, caller.ID, target.AppID); err != nil {
		t.Fatalf("authorize isolated preview sibling: %v", err)
	}
	if _, err := store.SetPreviewPrState(ctx, sibling.ID, state.PreviewPrStateStale); err != nil {
		t.Fatalf("retire preview sibling: %v", err)
	}
	target, ok, err = newServiceProxyResolver(store)(ctx, caller.ID, "billing")
	if err != nil || !ok || target.AppID != production.ID || target.PreviewScoped {
		t.Fatalf("resolve retired sibling = (%+v, %v, %v), want production fallback", target, ok, err)
	}
}

func TestServiceProxyResolverNeverCrossesPreviewScope(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "resolver-scope@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "resolver-scope"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	create := func(accountID, projectID string, app state.App) state.App {
		t.Helper()
		app.AccountID = accountID
		app.ProjectID = projectID
		app.RAMMB = 128
		app.Status = state.AppActive
		created, createErr := store.CreateApp(ctx, app)
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", app.Slug, createErr)
		}
		return created
	}
	caller := create(account.ID, project.ID, state.App{
		Slug: "pr-42-web", WorkloadName: "web", PreviewOfSlug: "web", PreviewPrNumber: 42,
	})
	production := create(account.ID, project.ID, state.App{Slug: "shipping", WorkloadName: "shipping"})
	otherPRPreview := create(account.ID, project.ID, state.App{
		Slug: "pr-43-shipping", WorkloadName: "shipping", PreviewOfSlug: production.Slug, PreviewPrNumber: 43,
	})

	otherProject, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "resolver-other-project"})
	if err != nil {
		t.Fatalf("CreateProject(other): %v", err)
	}
	searchProduction := create(account.ID, project.ID, state.App{Slug: "search", WorkloadName: "search"})
	_ = create(account.ID, otherProject.ID, state.App{
		Slug: "pr-42-other-search", WorkloadName: "search", PreviewOfSlug: searchProduction.Slug, PreviewPrNumber: 42,
	})

	otherAccount, err := store.CreateAccount(ctx, "resolver-other-account@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount(other): %v", err)
	}
	otherAccountProject, err := store.CreateProject(ctx, state.Project{AccountID: otherAccount.ID, Slug: "resolver-other-account"})
	if err != nil {
		t.Fatalf("CreateProject(other account): %v", err)
	}
	inventoryProduction := create(account.ID, project.ID, state.App{Slug: "inventory", WorkloadName: "inventory"})
	_ = create(otherAccount.ID, otherAccountProject.ID, state.App{
		Slug: "pr-42-other-inventory", WorkloadName: "inventory", PreviewOfSlug: inventoryProduction.Slug, PreviewPrNumber: 42,
	})

	resolve := newServiceProxyResolver(store)
	for _, tc := range []struct {
		service string
		wantID  string
	}{
		{"shipping", production.ID},
		{"search", searchProduction.ID},
		{"inventory", inventoryProduction.ID},
	} {
		target, ok, err := resolve(ctx, caller.ID, tc.service)
		if err != nil || !ok {
			t.Fatalf("resolve(%s) = (%+v, %v, %v)", tc.service, target, ok, err)
		}
		if target.AppID != tc.wantID || target.PreviewScoped {
			t.Errorf("resolve(%s) = %+v, want production %q", tc.service, target, tc.wantID)
		}
	}
	if target, ok, err := resolve(ctx, caller.ID, otherPRPreview.Slug); err != nil || ok {
		t.Fatalf("explicit other-PR slug resolved = (%+v, %v, %v), want not found", target, ok, err)
	}
}

func TestServiceProxyResolverPreservesProductionAndLegacyFallback(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "resolver-fallback@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	create := func(app state.App) state.App {
		t.Helper()
		app.AccountID = account.ID
		app.RAMMB = 128
		app.Status = state.AppActive
		created, createErr := store.CreateApp(ctx, app)
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", app.Slug, createErr)
		}
		return created
	}
	production := create(state.App{Slug: "mail", WorkloadName: "mail"})
	productionCaller := create(state.App{Slug: "worker", WorkloadName: "worker"})
	legacyPreview := create(state.App{
		Slug: "pr-42-worker", WorkloadName: "worker", PreviewOfSlug: productionCaller.Slug, PreviewPrNumber: 42,
	})
	developer := create(state.App{
		Slug: "dev-worker", WorkloadName: "worker", PreviewOfSlug: productionCaller.Slug, PreviewPrNumber: 0,
	})

	resolve := newServiceProxyResolver(store)
	for _, callerID := range []string{productionCaller.ID, legacyPreview.ID, developer.ID} {
		target, ok, err := resolve(ctx, callerID, "mail")
		if err != nil || !ok || target.AppID != production.ID || target.PreviewScoped {
			t.Errorf("resolve caller %q = (%+v, %v, %v), want production fallback %q", callerID, target, ok, err, production.ID)
		}
	}
}

func TestServiceProxyResolverAndPolicyUseProjectAliases(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "mapped-services@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	callers := []string{"web"}
	scopes := api.ServiceCallerScopes{"web": {Methods: []string{"GET"}, PathPrefixes: []string{"/checkout"}}}
	target, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-backend", WorkloadName: "api", Manifest: state.AppManifest{AllowedServiceCallers: &callers, AllowedServiceCallScopes: &scopes}})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-frontend", WorkloadName: "web", Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared, ServiceReliability: map[string]api.ServiceReliabilityPolicy{"api": {TimeoutMS: 800}}, ServiceBindings: []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_API_URL", Service: "api"}}}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok, err := newServiceProxyResolver(store)(t.Context(), caller.ID, "api")
	if err != nil || !ok || resolved.AppID != target.ID {
		t.Fatalf("wrong mapped target: %+v %v", resolved, err)
	}
	authorized, err := newServiceProxyAuthorizer(store)(t.Context(), caller.ID, resolved.AppID)
	if err != nil {
		t.Fatalf("logical dependency authorization failed: %v", err)
	}
	if authorized.CallScope == nil || authorized.CallScope.PathPrefixes[0] != "/checkout" || authorized.Reliability == nil || authorized.Reliability.TimeoutMS != 800 {
		t.Fatalf("logical dependency policies lost: %+v", authorized)
	}
	// Changing the caller's project cannot grant permission through its alias.
	otherProject, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	otherCaller, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: otherProject.ID, Slug: "other-web", WorkloadName: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newServiceProxyAuthorizer(store)(t.Context(), otherCaller.ID, target.ID); err == nil {
		t.Fatal("cross-project logical caller grant accepted")
	}
	preview, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-7-shop-frontend", WorkloadName: "web", PreviewOfSlug: caller.Slug, PreviewPrNumber: 7, Manifest: caller.Manifest})
	if err != nil {
		t.Fatal(err)
	}
	fallback, ok, err := newServiceProxyResolver(store)(t.Context(), preview.ID, "api")
	if err != nil || !ok || fallback.AppID != target.ID {
		t.Fatalf("mapped production fallback lost: %+v %v", fallback, err)
	}
	if _, err := newServiceProxyAuthorizer(store)(t.Context(), preview.ID, target.ID); err == nil {
		t.Fatal("preview fallback bypassed project policy")
	}
	policy := state.DefaultGitHubDeployPolicy(project.ID, account.ID)
	policy.PreviewServicePolicy = state.PreviewServicePolicyAllowMarked
	if _, err := store.UpsertGitHubDeployPolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := newServiceProxyAuthorizer(store)(t.Context(), preview.ID, target.ID); err != nil {
		t.Fatalf("permitted mapped preview fallback denied: %v", err)
	}
}
