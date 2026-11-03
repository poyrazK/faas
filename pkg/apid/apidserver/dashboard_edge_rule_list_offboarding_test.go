package apidserver_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30 (round 5): editing a list checks the apps
// whose rules use it, but a dashboard request carried no API principal, so
// that check was skipped, and the dashboard gate only checked the app in the
// URL. A creator removed from the company could still edit, under one of
// their own apps' pages, the list a company app's IP rule uses.
func TestDashboardEdgeRuleListEditAuthorizesTheAppsUsingIt(t *testing.T) {
	_, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	srv := apidserver.NewServerWithDeps(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, sessions, nil, 15*time.Minute, "")
	h := srv.Handler()
	ctx := t.Context()
	creator, err := store.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, creator.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(ctx, "acme-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "acme-api", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, Slug: "alice-own", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: creator.ID, Name: "blocked", Kind: "ip",
		Items: []string{"203.0.113.0/24"}}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: creator.ID, AppID: company.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindIP,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0"}}},
		Match:  &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "blocked"}}); err != nil {
		t.Fatal(err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, creator.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	edit := func(item string) int {
		t.Helper()
		rec := apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/alice-own/edge-rule-lists/blocked/update",
			map[string]string{middleware.FormFieldName: token, "items": item}, &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token})
		return rec.Code
	}
	if code := edit("198.51.100.0/24"); code != http.StatusSeeOther {
		t.Fatalf("a company admin's dashboard list edit = %d, want 303", code)
	}

	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if code := edit("192.0.2.0/24"); code == http.StatusSeeOther {
		t.Fatal("an offboarded creator edited the company app's list from the dashboard")
	}
	lists, err := store.EdgeRuleListsByName(ctx, creator.ID, []string{"blocked"})
	if err != nil || len(lists) != 1 || len(lists[0].Items) != 1 || lists[0].Items[0] != "198.51.100.0/24" {
		t.Fatalf("list after the refused edit = %+v, %v; want the member's edit kept", lists, err)
	}
}
