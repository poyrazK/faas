package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// suggestionTelemetryStore serves canned request telemetry: a noticeable
// bot share, a busy sign-in path and some /admin traffic.
type suggestionTelemetryStore struct {
	MemStore *state.MemStore
}

func (s *suggestionTelemetryStore) RequestTelemetryAnalyticsByDimension(context.Context, sqlc.RequestTelemetryAnalyticsByDimensionParams) ([]sqlc.RequestTelemetryAnalyticsByDimensionRow, error) {
	return []sqlc.RequestTelemetryAnalyticsByDimensionRow{{Dimension: "browser", Requests: 3000}, {Dimension: "bot", Requests: 900}}, nil
}

func (s *suggestionTelemetryStore) RequestTelemetryAnalyticsByRoute(context.Context, sqlc.RequestTelemetryAnalyticsByRouteParams) ([]sqlc.RequestTelemetryAnalyticsByRouteRow, error) {
	return []sqlc.RequestTelemetryAnalyticsByRouteRow{
		{Route: "POST /login", Method: "POST", Requests: 800, ErrorRequests: 200},
		{Route: "GET /admin", Method: "GET", Requests: 25},
	}, nil
}

// adr: 20261020105000 — the edge-rules page suggests recipes from the app's traffic,
// each a form for the recipes route with the suggested parameters filled
// in; applying one adds a log-mode rule and the suggestion goes away. Plans
// without request telemetry show no suggestions.
func TestDashboardEdgeRuleSuggestions(t *testing.T) {
	_, cookie, base, mgr := newAuthedDashboardServerFullFull(t, "hobby", "suggest@example.com")
	acct, err := base.AccountByEmail(t.Context(), "suggest@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := base.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "suggest-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	srv := apidserver.NewServerWithDeps(statetest.NewMem(&suggestionTelemetryStore{MemStore: base}), apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	h := srv.Handler()
	suggestions := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/suggest-app/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d", rec.Code)
		}
		body := rec.Body.String()
		start := strings.Index(body, `id="suggested-protections"`)
		if start < 0 {
			return ""
		}
		end := strings.Index(body[start:], `id="protection-recipes"`)
		if end < 0 {
			t.Fatal("suggestions section not followed by the recipes section")
		}
		return body[start : start+end]
	}

	section := suggestions()
	for _, want := range []string{
		"Suggested for this app", "Block unverified bots", "in the last 3 days",
		`name="recipe" value="rate-limit-path"`, `name="path" value="/login"`, `name="methods" value="POST"`,
		`name="recipe" value="allow-ips-only"`, `name="path" value="/admin/**"`, `name="cidrs"`, "Add in log mode",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("suggestions missing %q:\n%s", want, section)
		}
	}

	token, err := middleware.IssueForAuthenticatedNamed(mgr, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	form := map[string]string{middleware.FormFieldName: token, "recipe": "rate-limit-path", "path": "/login", "methods": "POST"}
	rec := apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/suggest-app/edge-rules/recipes", form, &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=recipe_log") {
		t.Fatalf("apply suggestion: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	rules, err := base.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil || len(rules) != 1 || rules[0].MatchPath != "/login" || rules[0].Mode != api.EdgeRuleModeLog || strings.Join(rules[0].MatchMethods, ",") != "POST" {
		t.Fatalf("rules = %+v, err = %v", rules, err)
	}
	if section := suggestions(); strings.Contains(section, `value="/login"`) || !strings.Contains(section, "Block unverified bots") {
		t.Fatalf("after applying, suggestions = %s", section)
	}

	if err := base.UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	if section := suggestions(); section != "" {
		t.Fatalf("free plan shows suggestions: %s", section)
	}
}

// adr: 20261020105000 — GET /v1/apps/{slug}/edge-rules/suggestions serves the same
// suggestions and is gated on plans without request telemetry.
func TestEdgeRuleSuggestionsRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "suggest-api")
	(*(*e.S()).Store()) = statetest.NewMem(&suggestionTelemetryStore{MemStore: *e.Store()})
	rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/suggestions", nil, nil)
	var out api.EdgeRuleRecipeSuggestionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("status %d, err %v: %s", rec.Code, err, rec.Body.String())
	}
	if out.Window != "7d" || len(out.Suggestions) != 3 || out.Suggestions[1].Params["path"] != "/login" {
		t.Fatalf("suggestions = %+v", out)
	}
	if rec := e.Do(t, "GET", "/v1/apps/missing/edge-rules/suggestions", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d, want 404", rec.Code)
	}

	free := apidserver.Setup(t, api.PlanFree)
	slug = apidserver.MustSeedEdgeRuleApp(t, free, "suggest-free")
	rec = free.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/suggestions", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanFeatureGated)
}
