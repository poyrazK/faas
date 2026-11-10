package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A push applies a functions-project entry's RAM, idle timeout, concurrency
// and env to its function app, and prune_env removes undeclared keys
// (ADR-20261108160000 §4).
func TestApplySourceFunctionSettings(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "fn-settings@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "shop-api", Type: state.AppTypeFunction, Runtime: "node22",
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, state.DefaultEnvScope, "STALE", "x"); err != nil {
		t.Fatal(err)
	}
	spec := gregalemanifest.FunctionSpec{
		Name: "api", RAMMB: 512, IdleTimeoutS: 120, Concurrency: 4,
		Env: map[string]string{"MODE": "prod"}, PruneEnv: true,
	}
	got, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, app, spec)
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if got.RAMMB != 512 || got.IdleTimeoutS != 120 || got.Manifest.FunctionConcurrency != 4 {
		t.Errorf("app = RAM %d idle %d concurrency %d, want 512/120/4", got.RAMMB, got.IdleTimeoutS, got.Manifest.FunctionConcurrency)
	}
	env, err := store.ListAppEnv(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0].Key != "MODE" || env[0].Value != "prod" {
		t.Errorf("env = %+v, want only MODE=prod", env)
	}

	// A RAM above the plan is refused with PATCH's validation.
	spec.RAMMB = 1 << 20
	if _, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, got, spec); prob == nil {
		t.Fatal("RAM above the plan must be refused")
	}
}
