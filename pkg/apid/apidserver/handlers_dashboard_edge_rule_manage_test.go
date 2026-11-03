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
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30: the dashboard could create, toggle and
// delete a rule but not edit one, and had no view of rule lists or of the
// rule-set history. It now edits a rule, manages lists and rolls back, each
// through the API handler.
func TestDashboardEdgeRulesEditListsAndRollback(t *testing.T) {
	_, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	srv := apidserver.NewServerWithDeps(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, sessions, nil, 15*time.Minute, "")
	h := srv.Handler()
	ctx := t.Context()
	acct, err := store.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "edge-manage", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, form map[string]string, wantAction string) {
		t.Helper()
		form[middleware.FormFieldName] = token
		rec := apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/edge-manage"+path, form, &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token})
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action="+wantAction) {
			t.Fatalf("POST %s: %d %s %s", path, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	}
	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/edge-manage/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d", rec.Code)
		}
		return rec.Body.String()
	}

	post("/edge-rules", map[string]string{"match_host": "@app", "match_path": "/**", "kind": "headers", "enabled": "on",
		"action": `{"response_headers":[{"name":"X-Edge","value":"one","action":"set"}]}`}, "created")
	rules, err := store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules after create = %d, %v", len(rules), err)
	}
	rule := rules[0]
	body := page()
	for _, want := range []string{`/edge-rules/` + rule.ID + `/edit`, `X-Edge`, "Rule lists", "Create a list", "History"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	post("/edge-rules/"+rule.ID+"/edit", map[string]string{"match_path": "/api/**", "match_methods": "GET", "priority": "7",
		"action": `{"response_headers":[{"name":"X-Edge","value":"two","action":"set"}]}`}, "updated")
	edited, err := store.GetEdgeRuleByID(ctx, rule.ID)
	if err != nil || edited.MatchPath != "/api/**" || edited.Priority != 7 || edited.Action.Headers == nil ||
		edited.Action.Headers.ResponseHeaders[0].Value != "two" {
		t.Fatalf("edited rule = %+v, %v", edited, err)
	}

	post("/edge-rule-lists", map[string]string{"name": "blocked", "kind": "ip", "items": "203.0.113.0/24\n198.51.100.7/32"}, "list_created")
	post("/edge-rule-lists/blocked/update", map[string]string{"items": "192.0.2.0/24"}, "list_updated")
	lists, err := store.EdgeRuleListsByName(ctx, acct.ID, []string{"blocked"})
	if err != nil || len(lists) != 1 || len(lists[0].Items) != 1 || lists[0].Items[0] != "192.0.2.0/24" {
		t.Fatalf("list after the edit = %+v, %v", lists, err)
	}
	if body := page(); !strings.Contains(body, "192.0.2.0/24") || !strings.Contains(body, "/edge-rule-lists/blocked/delete") {
		t.Error("page does not show the list's items and delete form")
	}
	post("/edge-rule-lists/blocked/delete", map[string]string{}, "list_deleted")
	if lists, _ := store.EdgeRuleListsByName(ctx, acct.ID, []string{"blocked"}); len(lists) != 0 {
		t.Fatalf("list still present after delete: %+v", lists)
	}

	// Version 1 is the rule as created; rolling back restores its path.
	if !strings.Contains(page(), "Roll back to this version") {
		t.Fatal("history offers no rollback")
	}
	post("/edge-rules/rollback", map[string]string{"version": "1"}, "rolled_back")
	restored, err := store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil || len(restored) != 1 || restored[0].MatchPath != "/**" {
		t.Fatalf("rules after rollback = %+v, %v; want the created rule's path", restored, err)
	}
}
