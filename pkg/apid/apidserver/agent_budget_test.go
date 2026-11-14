// adr: 20261111114059
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An agent's changes to an app's always-on capacity are charged to its
// monthly budget. Within the budget they go through; past it the change asks
// a person (category budget, with its added cost on the review page), and an
// approved change goes through once. The person can raise the budget; the
// agent cannot.
func TestHandlers_AgentBudget(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "budget-owner@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "warm-svc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5}); err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatalf("CreateAPIKey %s: %v", label, err)
		}
		return plaintext, key
	}
	agent, agentKey := mint("budget-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	// Only the budget asks: the agent chose no approval categories.
	if err := store.SetAPIKeyApprovalCategories(ctx, agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	owner, _ := mint("budget-admin", api.ScopeAdmin)
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	budget := func(key, path string) api.AgentBudgetResponse {
		t.Helper()
		rec := call(key, http.MethodGet, path, "")
		var out api.AgentBudgetResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
		return out
	}

	// One warm 512 MB instance: about $18.53 a month, within Pro's $25.
	if rec := call(agent, http.MethodPatch, "/v1/apps/warm-svc", `{"min_instances":1}`); rec.Code != http.StatusOK {
		t.Fatalf("first scale-up = %d %s", rec.Code, rec.Body)
	}
	got := budget(agent, "/v1/agent-connections/self/budget")
	wantFirst := api.CentsCeil(api.AgentStandingMillicents(1, 512))
	if got.MonthlyCents == nil || *got.MonthlyCents != api.PlanPro.AgentBudgetDefaultCents() || got.Source != api.AgentBudgetSourcePlanDefault || got.SpentCents != wantFirst {
		t.Fatalf("budget after first change = %+v; want plan default with %d spent", got, wantFirst)
	}

	// Doubling its RAM adds about $18.25 more: past the budget.
	const raise = `{"ram_mb":1024}`
	rec := call(agent, http.MethodPatch, "/v1/apps/warm-svc", raise)
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.AgentApproval == nil ||
		problem.AgentApproval.Category != api.AgentApprovalBudget || !strings.Contains(problem.Detail, "budget") {
		t.Fatalf("over-budget change = %d %s; want 428 budget", rec.Code, rec.Body)
	}
	if app, err := store.AppBySlug(ctx, "warm-svc"); err != nil || app.RAMMB != 512 {
		t.Fatalf("app after the refused change = %d MB, %v; want unchanged", app.RAMMB, err)
	}
	id := problem.AgentApproval.ID
	page, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, id, email)
	if body := page.Body.String(); !strings.Contains(body, "Added cost") || !strings.Contains(body, "a month") {
		t.Fatalf("review page does not show the added cost:\n%s", body)
	}
	if approved := apidserver.DecideAgentApprovalForTest(t, srv, id, cookie, form, "approve"); approved.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body)
	}
	if rec := call(agent, http.MethodPatch, "/v1/apps/warm-svc", raise); rec.Code != http.StatusOK {
		t.Fatalf("approved change = %d %s", rec.Code, rec.Body)
	}
	if got := budget(agent, "/v1/agent-connections/self/budget"); got.SpentCents != api.CentsCeil(api.AgentStandingMillicents(1, 1024)) {
		t.Fatalf("spent after the approved change = %d; want the full committed cost", got.SpentCents)
	}

	// The agent cannot raise its own budget; the person can.
	if rec := call(agent, http.MethodPut, "/v1/agent-connections/"+agentKey.ID+"/budget", `{"unlimited":true}`); rec.Code < 400 {
		t.Fatalf("agent raising its own budget = %d; want refused", rec.Code)
	}
	if rec := call(owner, http.MethodPut, "/v1/agent-connections/"+agentKey.ID+"/budget", `{"monthly_cents":10000,"unlimited":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("two budget choices = %d; want 400", rec.Code)
	}
	if rec := call(owner, http.MethodPut, "/v1/agent-connections/"+agentKey.ID+"/budget", `{"unlimited":true}`); rec.Code != http.StatusOK {
		t.Fatalf("owner sets unlimited = %d %s", rec.Code, rec.Body)
	}
	if got := budget(owner, "/v1/agent-connections/"+agentKey.ID+"/budget"); got.MonthlyCents != nil || !got.KeyUnlimited || got.Source != api.AgentBudgetSourceUnlimited {
		t.Fatalf("budget after unlimited = %+v", got)
	}
	if rec := call(agent, http.MethodPatch, "/v1/apps/warm-svc", `{"min_instances":2}`); rec.Code != http.StatusOK {
		t.Fatalf("unlimited agent's change = %d %s", rec.Code, rec.Body)
	}

	// A person's own key is never charged.
	if err := store.SetAgentKeyBudget(ctx, agentKey.ID, state.AgentKeyBudget{Set: true}, acct.ID); err != nil {
		t.Fatal(err)
	}
	if rec := call(owner, http.MethodPatch, "/v1/apps/warm-svc", `{"min_instances":1}`); rec.Code != http.StatusOK {
		t.Fatalf("person's change = %d %s", rec.Code, rec.Body)
	}
	// A zero budget asks for any added cost.
	if rec := call(agent, http.MethodPatch, "/v1/apps/warm-svc", `{"min_instances":3}`); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("change on a zero budget = %d %s; want 428", rec.Code, rec.Body)
	}
	// Lowering cost never asks.
	if rec := call(agent, http.MethodPatch, "/v1/apps/warm-svc", `{"min_instances":0}`); rec.Code != http.StatusOK {
		t.Fatalf("scale-down = %d %s", rec.Code, rec.Body)
	}
}
