package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// The dashboard's create form offered thirteen kinds and none of the access
// and protection ones, so a customer could only add an IP, JWT, WAF or
// challenge rule with the CLI or API. The form now offers all four, and the
// challenge only once it is released to the account (ADR-20261023113721).
func TestDashboardEdgeRuleCreateOffersAccessAndProtectionKinds(t *testing.T) {
	_, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	srv := apidserver.NewServerWithDeps(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, sessions, nil, 15*time.Minute, "")
	h := srv.Handler()
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "edge-kinds", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/edge-kinds/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d", rec.Code)
		}
		return rec.Body.String()
	}
	create := func(kind, path, action string) {
		t.Helper()
		rec := apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/edge-kinds/edge-rules", map[string]string{
			middleware.FormFieldName: token, "match_host": "@app", "match_path": path, "kind": kind, "action": action, "enabled": "on",
		}, &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token})
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=created") {
			t.Fatalf("create %s: %d %s %s", kind, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	}

	body := page()
	for _, want := range []string{`<option value="ip">`, `<option value="jwt">`, `<option value="waf">`, `<option value="challenge" disabled>`, `href="/docs/edge-rules"`} {
		if !strings.Contains(body, want) {
			t.Errorf("unreleased page missing %q", want)
		}
	}
	create("ip", "/admin/**", `{"deny":["203.0.113.0/24"]}`)
	create("jwt", "/api/**", `{"issuer":"https://idp.example.com/","jwks_url":"https://idp.example.com/.well-known/jwks.json","algorithms":["RS256"],"audience":["api"]}`)
	create("waf", "/**", `{"mode":"observe"}`)

	srv.SeedPlatformFlag(platformflags.EdgeChallenge, platformflags.Definition{Enabled: true, Default: true})
	if body := page(); !strings.Contains(body, `<option value="challenge">`) {
		t.Fatal("released page does not offer the challenge kind")
	}
	create("challenge", "/login", `{}`)

	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil {
		t.Fatalf("ListEdgeRulesForApp: %v", err)
	}
	kinds := map[state.EdgeRuleKind]bool{}
	for _, r := range rules {
		kinds[r.Kind] = true
	}
	for _, kind := range []state.EdgeRuleKind{state.EdgeRuleKindIP, state.EdgeRuleKindJWT, state.EdgeRuleKindWAF, state.EdgeRuleKindChallenge} {
		if !kinds[kind] {
			t.Errorf("no %s rule created; rules = %+v", kind, rules)
		}
	}
}
