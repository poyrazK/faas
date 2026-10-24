// adr: 1252
package mcpmanagement

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type budgetFixture struct {
	mu               sync.Mutex
	store            *state.MemStore
	account          state.Account
	app              state.App
	project          state.Project
	env              state.ProjectEnvironment
	ambiguous        bool
	writes           int
	receiptReads     int
	receiptStatus    int
	receiptTransform func(*api.EnvironmentRouteBudgetReceipt)
}

func newBudgetFixture(t *testing.T) (*budgetFixture, *httptest.Server, Options) {
	t.Helper()
	f := &budgetFixture{store: state.NewMemStore()}
	var err error
	f.account, err = f.store.CreateAccount(t.Context(), "budget@test.example", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = f.store.CreateProject(t.Context(), state.Project{AccountID: f.account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "alpha", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	f.env, err = f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	b := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(b.Close)
	opts := Options{APIBase: b.URL, Token: testToken, AllowWrites: true, AllowedApps: map[string]string{"alpha": f.app.ID}, Environments: &EnvironmentConsent{Named: map[string]EnvironmentGrant{"shop/staging": {Project: "shop", ProjectID: f.project.ID, Environment: "staging", EnvironmentID: f.env.ID, Apps: map[string]string{"alpha": f.app.ID}, Write: true}}}}
	return f, b, opts
}
func (f *budgetFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	if strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/route-budgets/receipts/") && r.Method == http.MethodGet {
		f.receiptReads++
		if f.receiptStatus != 0 {
			w.WriteHeader(f.receiptStatus)
			return
		}
		scope := r.URL.Query().Get("environment")
		if scope == "" && r.URL.Query().Get("scope") == "default" {
			scope = "default"
		}
		out, err := f.store.GetEnvironmentRouteBudgetReceipt(r.Context(), f.account.ID, f.app.ID, scope, strings.TrimPrefix(r.URL.Path, "/v1/apps/alpha/route-budgets/receipts/"))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if f.receiptTransform != nil {
			f.receiptTransform(&out)
		}
		encode(out)
		return
	}
	switch r.URL.Path {
	case "/v1/account":
		encode(api.AccountResponse{ID: f.account.ID})
	case "/v1/apps/alpha":
		encode(api.AppResponse{ID: f.app.ID, Slug: "alpha", Type: "app", RAMMB: 256})
	case "/v1/projects/shop":
		encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: f.project.ID, Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "alpha"}}})
	case "/v1/projects/shop/environments/staging":
		encode(api.ProjectEnvironmentResponse{ID: f.env.ID, ProjectID: f.project.ID, Slug: "staging"})
	case "/v1/apps/alpha/route-budgets":
		scope := r.URL.Query().Get("environment")
		if scope == "" && r.URL.Query().Get("scope") == "default" {
			scope = "default"
		}
		if scope == "" {
			w.WriteHeader(400)
			return
		}
		if r.Method == http.MethodGet {
			out, err := f.store.GetEnvironmentRouteBudgets(r.Context(), f.account.ID, f.app.ID, scope)
			if err != nil {
				w.WriteHeader(404)
				return
			}
			encode(out)
			return
		}
		var req api.SetEnvironmentRouteBudgetsRequest
		if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&req) != nil || r.Header.Get("Idempotency-Key") != req.RequestID {
			w.WriteHeader(400)
			return
		}
		out, err := f.store.SetEnvironmentRouteBudgets(r.Context(), f.account.ID, f.app.ID, scope, req)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Changed", "budget revision changed"))
			return
		}
		f.writes++
		if f.ambiguous {
			f.ambiguous = false
			w.WriteHeader(504)
			return
		}
		encode(out)
	default:
		w.WriteHeader(404)
	}
}
func budgetArgs() map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "reason": "Review staging latency budget", "inherit": false, "enabled": true, "routes": []map[string]any{{"method": "GET", "path": "/checkout", "max_p95_ms": 500}}}
}

func TestRouteBudgetPlanConsentStaleAndInheritance(t *testing.T) {
	f, b, opts := newBudgetFixture(t)
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_get_route_budgets", map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}, false)
	if string(out["budget_source"]) != `"app_policy"` {
		t.Fatal("default inheritance missing")
	}
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
	if f.writes != 0 {
		t.Fatal("planning mutated budgets")
	}
	out = callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": plan}, false)
	if string(out["budget_source"]) != `"environment_policy"` || string(out["revision"]) != "1" {
		t.Fatal("override missing")
	}
	// Cached result is replayed without another write.
	callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": plan}, false)
	if f.writes != 1 {
		t.Fatal("cached apply wrote again")
	}
	stale := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
	newer := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
	callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": newer}, false)
	callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": stale}, true)
	// A plan on default remains separately denied by write consent.
	args := budgetArgs()
	delete(args, "project")
	delete(args, "environment")
	defaultPlan := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", args, false))
	callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": defaultPlan}, true)
	args = budgetArgs()
	args["inherit"] = true
	args["enabled"] = false
	args["routes"] = []any{}
	inherited := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", args, false))
	out = callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": inherited}, false)
	if string(out["budget_source"]) != `"app_policy"` {
		t.Fatal("inheritance was not restored")
	}
	neighbor, err := f.store.GetEnvironmentRouteBudgets(ctx, f.account.ID, f.app.ID, "default")
	if err != nil || neighbor.Revision != 0 {
		t.Fatal("staging altered default budget")
	}
}

func TestRouteBudgetLostReplyEncryptedRestart(t *testing.T) {
	f, b, opts := newBudgetFixture(t)
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "budget-grant"
	cs, ctx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
	plan := planID(t, callTool(t, ctx, cs, "gregale_plan_route_budgets", budgetArgs(), false))
	f.mu.Lock()
	f.ambiguous = true
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": plan}, true)
	_ = cs.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, b, opts, "2026-07-28")
	out := callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": plan}, false)
	if string(out["revision"]) != "1" {
		t.Fatal("restart did not recover original receipt")
	}
	current, err := f.store.GetEnvironmentRouteBudgets(ctx, f.account.ID, f.app.ID, "staging")
	if err != nil || current.Revision != 1 {
		t.Fatal("retry created another revision")
	}
	// A new identity cannot inherit even a cached successful result.
	f.mu.Lock()
	f.env.ID = uuid.NewString()
	f.mu.Unlock()
	callTool(t, ctx, cs, "gregale_apply_route_budgets", map[string]any{"plan_id": plan}, true)
}
