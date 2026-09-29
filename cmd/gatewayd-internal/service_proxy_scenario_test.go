package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceProxyScenarioTestNamespace(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "scenario-proxy@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	create := func(slug string, preview bool) state.App {
		t.Helper()
		app := state.App{AccountID: account.ID, Slug: slug, RAMMB: 128, Status: state.AppActive}
		if slug == "dev-scenario-gateway" {
			app.Manifest = state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared,
				ServiceBindings: []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_WORKER_URL", Service: "worker"}}}
		}
		if slug == "dev-scenario-worker" {
			allowed := []string{"gateway"}
			app.Manifest = state.AppManifest{AllowedServiceCallers: &allowed}
		}
		if preview {
			app.PreviewOfSlug = slug
			app.PreviewPrState = state.PreviewPrStateOpen
			app.PreviewExpiresAt = &expiry
		}
		created, createErr := store.CreateApp(ctx, app)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return created
	}
	caller := create("dev-scenario-gateway", true)
	worker := create("dev-scenario-worker", true)
	production := create("billing", false)
	runID := "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, runID, []state.ScenarioTestMember{
		{Workload: "gateway", AppID: caller.ID}, {Workload: "worker", AppID: worker.ID},
	}); err != nil {
		t.Fatal(err)
	}
	resolve := newServiceProxyResolver(store)
	authorize := newServiceProxyAuthorizer(store)
	target, ok, err := resolve(ctx, caller.ID, "worker")
	if err != nil || !ok || target.AppID != worker.ID || !target.PreviewScoped {
		t.Fatalf("sibling = (%+v, %v, %v)", target, ok, err)
	}
	if _, err := authorize(ctx, caller.ID, worker.ID); err != nil {
		t.Fatalf("authorize sibling: %v", err)
	}
	for _, name := range []string{"billing", worker.Slug} {
		if target, ok, err := resolve(ctx, caller.ID, name); err != nil || ok {
			t.Fatalf("escaped name %q = (%+v, %v, %v)", name, target, ok, err)
		}
	}
	if _, err := authorize(ctx, caller.ID, production.ID); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("test to production = %v", err)
	}
	if _, err := authorize(ctx, production.ID, worker.ID); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("production to test = %v", err)
	}
}
