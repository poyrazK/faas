// adr: 271
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceAliasAllowedUsesCurrentBindingInventory(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "alias@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "frontend", Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive,
		Manifest: state.AppManifest{
			ServiceBindingPolicy: api.ServiceBindingPolicyAccount,
			ServiceBindings:      []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := newServiceAliasAllowed(store)
	for _, tc := range []struct {
		caller string
		name   string
		want   bool
	}{
		{caller.ID, "billing", true},
		{caller.ID, "identity", false},
		{"not-a-uuid", "billing", false},
		{"00000000-0000-4000-8000-000000000000", "billing", false},
	} {
		got, err := allowed(ctx, tc.caller, tc.name)
		if err != nil || got != tc.want {
			t.Fatalf("alias %q/%q = %v, %v; want %v", tc.caller, tc.name, got, err, tc.want)
		}
	}
	manifest := caller.Manifest
	manifest.ServiceBindings = nil
	if _, err := store.UpdateApp(ctx, caller.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if got, err := allowed(ctx, caller.ID, "billing"); err != nil || got {
		t.Fatalf("removed binding still allowed: %v, %v", got, err)
	}
	manifest.ServiceBindings = []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}}
	deleted := state.AppDeleted
	if _, err := store.UpdateApp(ctx, caller.ID, state.UpdateAppParams{Manifest: &manifest, Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if got, err := allowed(ctx, caller.ID, "billing"); err != nil || got {
		t.Fatalf("deleted caller alias allowed: %v, %v", got, err)
	}
}

// ADR-20261025121530: a member of a registered developer or test namespace
// (`gregale dev --all`, scenario runs) names its siblings by .internal; a
// session outside the namespace does not.
func TestServiceAliasAllowedNamesNamespaceSiblings(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "namespace-alias@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	session := func(slug, project string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128,
			Status: state.AppActive, PreviewOfSlug: project, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	web, api1, loner, ui := session("dev-web-1", "web"), session("dev-api-1", "api"), session("dev-other-1", "other"), session("dev-ui-1", "app-ui")
	runID := "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, runID, []state.ScenarioTestMember{
		{Workload: "web", AppID: web.ID}, {Workload: "api", AppID: api1.ID}, {Workload: "ui", AppID: ui.ID}, // ADR-20261026105459: any service name
	}); err != nil {
		t.Fatal(err)
	}
	allowed := newServiceAliasAllowed(store)
	for _, tc := range []struct {
		caller, service string
		want            bool
	}{
		{web.ID, "api", true},
		{api1.ID, "web", true},
		{web.ID, "ui", true},
		{ui.ID, "api", true},
		{web.ID, "billing", false},
		{loner.ID, "api", false},
	} {
		if got, err := allowed(ctx, tc.caller, tc.service); err != nil || got != tc.want {
			t.Errorf("alias %q from %s = %v, %v; want %v", tc.service, tc.caller, got, err, tc.want)
		}
	}
}

type failingServiceAliasStore struct{ state.Store }

func (f failingServiceAliasStore) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, errors.New("store unavailable")
}

func TestServiceAliasAllowedStoreFailure(t *testing.T) {
	allowed := newServiceAliasAllowed(failingServiceAliasStore{Store: state.NewMemStore()})
	if ok, err := allowed(context.Background(), "00000000-0000-4000-8000-000000000001", "billing"); err == nil || ok {
		t.Fatalf("store failure = %v, %v", ok, err)
	}
}

type qualificationServiceAliasTestStore struct {
	state.Store
	allowed bool
	err     error
}

func (s qualificationServiceAliasTestStore) EnvironmentQualificationServiceAliasAllowed(context.Context, string, string) (bool, error) {
	return s.allowed, s.err
}

func TestServiceAliasAllowedUsesScopedQualificationBinding(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "qualification-alias@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	caller, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "frontend", Type: state.AppTypeApp, RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	allowed := newServiceAliasAllowed(qualificationServiceAliasTestStore{Store: store, allowed: true})
	if ok, err := allowed(ctx, caller.ID, "backend"); err != nil || !ok {
		t.Fatalf("scoped qualification alias = %v, %v", ok, err)
	}
	failure := errors.New("qualification store unavailable")
	allowed = newServiceAliasAllowed(qualificationServiceAliasTestStore{Store: store, err: failure})
	if ok, err := allowed(ctx, caller.ID, "backend"); err == nil || ok || !errors.Is(err, failure) {
		t.Fatalf("qualification alias store failure = %v, %v", ok, err)
	}
}

// ADR-20261022160248: under the project policy the alias names every service
// of the caller's own project, as the proxy already admits those calls.
func TestServiceAliasAllowedNamesProjectServicesUnderProjectPolicy(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "project-alias@local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "search"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(app state.App) state.App {
		t.Helper()
		app.AccountID, app.Type, app.RAMMB = account.ID, state.AppTypeApp, 128
		if app.Status == "" {
			app.Status = state.AppActive
		}
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("CreateApp(%s): %v", app.Slug, err)
		}
		return created
	}
	projectPolicy := state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyProject}
	caller := create(state.App{ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Manifest: projectPolicy})
	create(state.App{ProjectID: project.ID, Slug: "shop-billing", WorkloadName: "billing"})
	create(state.App{ProjectID: project.ID, Slug: "shop-ledger", WorkloadName: "ledger", Status: state.AppDeleted})
	create(state.App{ProjectID: other.ID, Slug: "search-index", WorkloadName: "index"})
	create(state.App{Slug: "standalone"})
	declared := create(state.App{ProjectID: project.ID, Slug: "shop-worker", WorkloadName: "worker",
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared}})
	preview := create(state.App{ProjectID: project.ID, Slug: "pr-7-shop-web", WorkloadName: "web",
		PreviewOfSlug: caller.Slug, PreviewPrNumber: 7, Manifest: projectPolicy})
	scenario := create(state.App{ProjectID: project.ID, Slug: "test-shop-web", WorkloadName: "web",
		PreviewOfSlug: caller.Slug, Manifest: projectPolicy})

	allowed := newServiceAliasAllowed(store)
	// Production asks through the pinned service-policy view, which has only
	// the indexed project lookup; on rc.297 it refused every undeclared
	// sibling alias with 403 (Hunt 14 H14-81).
	pin := newServicePolicyPinner(serviceSnapshotStoreFixture{reader: store})
	for _, tc := range []struct {
		name, caller, service string
		want                  bool
	}{
		{"sibling by service name", caller.ID, "billing", true},
		{"sibling by app slug", caller.ID, "shop-billing", true},
		{"service names are case-insensitive", caller.ID, "Billing", true},
		{"deleted sibling", caller.ID, "ledger", false},
		{"another project's service", caller.ID, "search-index", false},
		{"standalone app", caller.ID, "standalone", false},
		{"unknown name", caller.ID, "payments", false},
		{"declared policy keeps the binding inventory", declared.ID, "billing", false},
		{"PR preview names its project's services", preview.ID, "billing", true},
		{"scenario-test member keeps the binding inventory", scenario.ID, "billing", false},
	} {
		got, err := allowed(ctx, tc.caller, tc.service)
		if err != nil || got != tc.want {
			t.Errorf("%s: alias %q = %v, %v; want %v", tc.name, tc.service, got, err, tc.want)
		}
		snapshot, err := pin(ctx, tc.caller, tc.service, true)
		if err != nil || snapshot.AliasAllowed != tc.want {
			t.Errorf("%s: pinned alias %q = %v, %v; want %v", tc.name, tc.service, snapshot.AliasAllowed, err, tc.want)
		}
	}
	sibling, err := pin(ctx, caller.ID, "billing", true)
	if err != nil || !sibling.Found || sibling.AuthorizationError != nil || sibling.Target.AppID == "" {
		t.Fatalf("pinned sibling call = %+v, %v; want a found, authorized target", sibling, err)
	}
}
