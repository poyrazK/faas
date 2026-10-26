package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261026170512: a Compose service named billing_api (or BILLING_API)
// is called billing-api. The URL its dependents get, the name lookup, the
// declared-binding check, the caller allowlist, the reliability policy, the
// .internal alias and the PR-preview sibling all agree on that form.
func TestServiceCallsUseServiceDNSNames(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "dns-names@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	callers := []string{"web-ui"}
	// A project-prefixed slug, so the bare-slug fallback cannot hide a miss.
	target, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-billing", WorkloadName: "billing_api",
		Manifest: state.AppManifest{AllowedServiceCallers: &callers}})
	if err != nil {
		t.Fatal(err)
	}
	bindings := []api.AppServiceBinding{{Binding: api.ServiceBindingEnvKey("billing_api"), Service: "billing_api"}}
	caller, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "Web_UI",
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared, ServiceBindings: bindings,
			ServiceReliability: map[string]api.ServiceReliabilityPolicy{"billing-api": {TimeoutMS: 700}}}})
	if err != nil {
		t.Fatal(err)
	}

	env := api.ServiceBindingEnv(nil, bindings)
	if got := env["GREGALE_SERVICE_BILLING_API_URL"]; got != "http://billing-api.svc.gregale:10081" {
		t.Fatalf("injected URL = %q", got)
	}
	if got := env["GREGALE_SERVICE_BILLING_API_HOST"]; got != "billing-api.svc.gregale" {
		t.Fatalf("injected host = %q", got)
	}

	resolved, ok, err := newServiceProxyResolver(store)(ctx, caller.ID, "billing-api")
	if err != nil || !ok || resolved.AppID != target.ID {
		t.Fatalf("billing-api resolved to %+v (%t, %v), want the billing_api service", resolved, ok, err)
	}
	authorized, err := newServiceProxyAuthorizer(store)(ctx, caller.ID, target.ID)
	if err != nil {
		t.Fatalf("declared billing_api dependency, Web_UI caller allowlisted as web-ui: %v", err)
	}
	if authorized.Reliability == nil || authorized.Reliability.TimeoutMS != 700 {
		t.Fatalf("reliability policy lost: %+v", authorized)
	}
	if allowed, err := newServiceAliasAllowed(store)(ctx, caller.ID, "billing-api"); err != nil || !allowed {
		t.Fatalf("billing-api.internal = %t, %v", allowed, err)
	}

	// A PR preview finds its own PR's sibling by the same name.
	previewTarget, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-7-shop-billing", WorkloadName: "billing_api",
		PreviewOfSlug: target.Slug, PreviewPrNumber: 7, PreviewPrState: state.PreviewPrStateOpen})
	if err != nil {
		t.Fatal(err)
	}
	previewCaller, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-7-shop-web", WorkloadName: "Web_UI",
		PreviewOfSlug: caller.Slug, PreviewPrNumber: 7, PreviewPrState: state.PreviewPrStateOpen, Manifest: caller.Manifest})
	if err != nil {
		t.Fatal(err)
	}
	sibling, ok, err := newServiceProxyResolver(store)(ctx, previewCaller.ID, "billing-api")
	if err != nil || !ok || sibling.AppID != previewTarget.ID {
		t.Fatalf("preview resolved billing-api to %+v (%t, %v), want its own PR's sibling", sibling, ok, err)
	}
}
