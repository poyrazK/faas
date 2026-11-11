package apidserver_test

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
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
	got, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, app, spec, "")
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
	// The push's env change is one config revision attributed to the pusher,
	// so `gregale env history` shows it and config rollback can undo it.
	revisions, err := store.ListConfigRevisions(ctx, app.ID, state.DefaultEnvScope, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) == 0 || revisions[0].Source != state.ConfigSourceEnvBatch || revisions[0].Actor != "github:octocat" ||
		revisions[0].Variables["MODE"] != "prod" || revisions[0].Variables["STALE"] != "" {
		t.Errorf("latest revision = %+v, want env.batch by github:octocat with MODE=prod and no STALE", revisions)
	}

	// Snapshot hooks declared in code wire the app's lifecycle to the runner
	// (ADR-20261110190000 §5).
	hooked := gregalemanifest.FunctionSpec{Name: "api", Hooks: []string{"init", "after_restore"}}
	withHooks, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, got, hooked, "")
	if prob != nil {
		t.Fatalf("apply hooks: %+v", prob)
	}
	if withHooks.Manifest.AfterRestore == nil || withHooks.Manifest.AfterRestore.Path != functionlifetime.FunctionAfterRestorePath || withHooks.Manifest.BeforeCheckpoint != nil {
		t.Fatalf("lifecycle = after %+v before %+v, want only the reserved after-restore path", withHooks.Manifest.AfterRestore, withHooks.Manifest.BeforeCheckpoint)
	}
	if withHooks.RAMMB != 512 {
		t.Errorf("hooks-only push changed RAM to %d", withHooks.RAMMB)
	}

	// A RAM above the plan is refused with PATCH's validation.
	over := spec
	over.RAMMB = 1 << 20
	if _, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, got, over, ""); prob == nil {
		t.Fatal("RAM above the plan must be refused")
	}

	// A push to a branch mapped to staging writes its env in staging only,
	// prunes only staging, and leaves the app-wide settings production runs
	// with alone.
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "staging", "OLD", "y"); err != nil {
		t.Fatal(err)
	}
	staging := gregalemanifest.FunctionSpec{
		Name: "api", RAMMB: 1024, IdleTimeoutS: 30, Concurrency: 8,
		Env: map[string]string{"MODE": "staging"}, PruneEnv: true,
	}
	after, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, got, staging, "staging")
	if prob != nil {
		t.Fatalf("staging apply: %+v", prob)
	}
	if after.RAMMB != 512 || after.IdleTimeoutS != 120 || after.Manifest.FunctionConcurrency != 4 {
		t.Errorf("a staging push changed app-wide settings: RAM %d idle %d concurrency %d", after.RAMMB, after.IdleTimeoutS, after.Manifest.FunctionConcurrency)
	}
	prod, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, state.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(prod) != 1 || prod[0].Key != "MODE" || prod[0].Value != "prod" {
		t.Errorf("production env = %+v, want MODE=prod untouched", prod)
	}
	stg, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(stg) != 1 || stg[0].Key != "MODE" || stg[0].Value != "staging" {
		t.Errorf("staging env = %+v, want only MODE=staging", stg)
	}
}

// ADR-20261111150000 §2: a production push binds a function to its project
// siblings and `calls:` apps and writes the GREGALE_FUNCTIONS map; an app
// under the project policy keeps it.
func TestApplySourceFunctionSettingsBindsSiblings(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "fn-calls@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug, workload string, policy api.ServiceBindingPolicy) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{
			AccountID: acct.ID, Slug: slug, Type: state.AppTypeFunction, Runtime: "node22", ProjectID: project.ID, WorkloadName: workload,
			RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, WorkloadClass: state.WorkloadClassHTTP,
			Manifest: state.AppManifest{ServiceBindingPolicy: policy},
		})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	web := create("shop-web", "web", api.ServiceBindingPolicyAccount)
	users := create("shop-users", "users", api.ServiceBindingPolicyProject)
	spec := gregalemanifest.FunctionSpec{Name: "web", Calls: []string{"billing-api"}, ProjectFunctions: []string{"web", "users"}}
	got, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, web, spec, "")
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	var targets []string
	for _, b := range got.Manifest.ServiceBindings {
		targets = append(targets, b.Service)
	}
	if !slices.Equal(targets, []string{"billing-api", "shop-users"}) || got.Manifest.ServiceBindingPolicy != api.ServiceBindingPolicyDeclared {
		t.Fatalf("web bindings = %v policy %q, want billing-api + shop-users under declared", targets, got.Manifest.ServiceBindingPolicy)
	}
	env, err := store.ListAppEnvInScope(ctx, acct.ID, web.ID, state.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0].Key != gregalemanifest.FunctionsEnv || env[0].Value != `{"billing-api":"billing-api","users":"shop-users"}` {
		t.Fatalf("web env = %+v", env)
	}

	gotUsers, prob := apidserver.ApplySourceFunctionSettingsForTest(ctx, store, acct, users, gregalemanifest.FunctionSpec{Name: "users", ProjectFunctions: []string{"web", "users"}}, "")
	if prob != nil {
		t.Fatalf("apply users: %+v", prob)
	}
	if gotUsers.Manifest.ServiceBindingPolicy != api.ServiceBindingPolicyProject || len(gotUsers.Manifest.ServiceBindings) != 1 || gotUsers.Manifest.ServiceBindings[0].Service != "shop-web" {
		t.Fatalf("users = policy %q bindings %+v, want project kept and shop-web bound", gotUsers.Manifest.ServiceBindingPolicy, gotUsers.Manifest.ServiceBindings)
	}
}
