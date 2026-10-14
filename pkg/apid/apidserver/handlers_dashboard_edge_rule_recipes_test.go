package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 986 — a protection recipe adds one ordinary rule through the API
// create path, in log mode unless the form enforces it; the page lists the
// recipes, marks added ones, and explains plan gates.
func TestDashboardEdgeRuleRecipes(t *testing.T) {
	h, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "recipe-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	csrf := &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token}
	post := func(recipe string, form map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		form[middleware.FormFieldName] = token
		form["recipe"] = recipe
		return apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/recipe-app/edge-rules/recipes", form, csrf)
	}
	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/recipe-app/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d", rec.Code)
		}
		return rec.Body.String()
	}

	// Free plan: IP-based recipes explain the plan gate; the throttle one is offered.
	body := page()
	for _, want := range []string{"Protection recipes", "Block unverified bots", "Available on the Hobby plan and above", `name="recipe" value="rate-limit-path"`} {
		if !strings.Contains(body, want) {
			t.Errorf("free page missing %q", want)
		}
	}
	if strings.Contains(body, `name="recipe" value="block-unverified-bots"`) {
		t.Error("free page offers an IP recipe form")
	}

	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanHobby); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	if rec := post("block-unverified-bots", map[string]string{}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=recipe_log") {
		t.Fatalf("bots recipe: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if rec := post("allow-ips-only", map[string]string{"cidrs": "203.0.113.7", "enforce": "1"}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=recipe_enforce") {
		t.Fatalf("allow-ips recipe: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil || len(rules) != 2 {
		t.Fatalf("rules = %d, err = %v", len(rules), err)
	}
	modes := map[string]string{}
	for _, r := range rules {
		if r.Kind != state.EdgeRuleKindIP || r.Match == nil || !strings.HasSuffix(r.Description, "protection recipe.") || r.MatchHost != api.EdgeRuleAppHosts {
			t.Fatalf("rule = %+v", r)
		}
		modes[r.Name] = r.Mode
	}
	if modes["Block unverified bots"] != api.EdgeRuleModeLog || modes["Allow only listed IPs on a path"] != api.EdgeRuleModeEnforce {
		t.Fatalf("modes = %v", modes)
	}
	body = page()
	if !strings.Contains(body, "Block unverified bots</strong> <span class=\"muted\">· added") {
		t.Error("page does not mark the added recipe")
	}
	// adr: 1191 — the hosts section counts app-wide rules on every host and
	// app-wide rules show as covering every app host.
	if !strings.Contains(body, "Hosts") || !strings.Contains(body, "2 rules apply") || !strings.Contains(body, "every app host") {
		t.Error("page missing host coverage for app-wide rules")
	}

	for name, tc := range map[string]struct {
		recipe string
		form   map[string]string
		status int
	}{
		"unknown recipe":    {"nope", map[string]string{}, http.StatusNotFound},
		"missing countries": {"block-countries", map[string]string{}, http.StatusBadRequest},
		"bad cidr":          {"allow-ips-only", map[string]string{"cidrs": "not-an-ip"}, http.StatusBadRequest},
	} {
		if rec := post(tc.recipe, tc.form); rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tc.status, rec.Body.String())
		}
	}
}
