// adr: 20261022111119 — the project policy lets a workload call its own
// project's services without declaring them.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyAuthorizerProjectPolicy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "project-policy-authz@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "project-policy-authz"})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "project-policy-authz-other"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(app state.App) state.App {
		t.Helper()
		app.AccountID, app.Type, app.RAMMB, app.Status = account.ID, state.AppTypeApp, 128, state.AppActive
		created, createErr := store.CreateApp(ctx, app)
		if createErr != nil {
			t.Fatalf("CreateApp(%s): %v", app.Slug, createErr)
		}
		return created
	}
	projectPolicy := state.AppManifest{
		ServiceBindingPolicy: api.ServiceBindingPolicyProject,
		ServiceBindings:      []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_LEDGER_URL", Service: "ledger"}},
	}
	onlyWeb := []string{"web"}
	caller := create(state.App{ProjectID: project.ID, Slug: "pp-api", WorkloadName: "api", Manifest: projectPolicy})
	sibling := create(state.App{ProjectID: project.ID, Slug: "pp-billing", WorkloadName: "billing"})
	guardedSibling := create(state.App{ProjectID: project.ID, Slug: "pp-audit", WorkloadName: "audit",
		Manifest: state.AppManifest{AllowedServiceCallers: &onlyWeb}})
	otherProjectService := create(state.App{ProjectID: otherProject.ID, Slug: "pp-search", WorkloadName: "search"})
	declaredOtherProjectService := create(state.App{ProjectID: otherProject.ID, Slug: "ledger", WorkloadName: "ledger"})
	standalone := create(state.App{Slug: "pp-standalone"})
	standaloneCaller := create(state.App{Slug: "pp-standalone-caller", Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyProject}})
	declaredCaller := create(state.App{ProjectID: project.ID, Slug: "pp-worker", WorkloadName: "worker",
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared}})
	siblingPreview := create(state.App{ProjectID: project.ID, Slug: "pr-7-pp-billing", WorkloadName: "billing",
		PreviewOfSlug: sibling.Slug, PreviewPrNumber: 7})
	previewCaller := create(state.App{ProjectID: project.ID, Slug: "pr-7-pp-api", WorkloadName: "api",
		PreviewOfSlug: caller.Slug, PreviewPrNumber: 7, Manifest: projectPolicy})
	otherPRPreview := create(state.App{ProjectID: project.ID, Slug: "pr-8-pp-billing", WorkloadName: "billing",
		PreviewOfSlug: sibling.Slug, PreviewPrNumber: 8})
	authorize := newServiceProxyAuthorizer(store)

	tests := []struct {
		name           string
		caller, target state.App
		want           error
	}{
		{"same-project service needs no declaration", caller, sibling, nil},
		{"target caller list still applies", caller, guardedSibling, gateway.ErrServiceProxyCallerDenied},
		{"other project's service needs a declaration", caller, otherProjectService, gateway.ErrServiceProxyBindingDenied},
		{"declared service outside the project is allowed", caller, declaredOtherProjectService, nil},
		{"standalone service needs a declaration", caller, standalone, gateway.ErrServiceProxyBindingDenied},
		{"caller without a project has no project to share", standaloneCaller, standalone, gateway.ErrServiceProxyBindingDenied},
		{"declared policy keeps its edge-by-edge check", declaredCaller, sibling, gateway.ErrServiceProxyBindingDenied},
		{"production caller cannot reach a sibling's preview", caller, siblingPreview, gateway.ErrServiceProxyBindingDenied},
		{"preview reaches its own PR's sibling", previewCaller, siblingPreview, nil},
		{"preview never reaches another PR", previewCaller, otherPRPreview, gateway.ErrServiceProxyDenied},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := authorize(ctx, tc.caller.ID, tc.target.ID)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("authorize(%s -> %s) = %v, want %v", tc.caller.Slug, tc.target.Slug, err, tc.want)
			}
		})
	}
}
