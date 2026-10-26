package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func challengeRule(accountID, appID string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID: accountID, AppID: appID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/login", Priority: 10, Enabled: true,
		Kind:   state.EdgeRuleKindChallenge,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindChallenge, Challenge: &state.EdgeRuleChallengeAction{Difficulty: 16, PassTTLSeconds: 3600}},
	}
}

// The dashboard gate authorizes the caller for {slug} only, so deleting a rule
// through one app's page must not reach another app's rule: company apps and
// their creator's personal apps share AccountID.
func TestDashboardEdgeRuleDeleteStaysOnItsApp(t *testing.T) {
	h, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	newApp("mine")
	other := newApp("other")
	rule, err := store.CreateEdgeRule(t.Context(), challengeRule(acct.ID, other.ID))
	if err != nil {
		t.Fatal(err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	csrf := &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token}
	del := func(slug string) int {
		return apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/"+slug+"/edge-rules/"+rule.ID+"/delete",
			map[string]string{middleware.FormFieldName: token}, csrf).Code
	}
	if code := del("mine"); code != http.StatusNotFound {
		t.Fatalf("delete through another app's page = %d, want 404", code)
	}
	if _, err := store.GetEdgeRuleByID(t.Context(), rule.ID); err != nil {
		t.Fatalf("the other app's rule is gone: %v", err)
	}
	if code := del("other"); code != http.StatusSeeOther {
		t.Fatalf("delete through the rule's own app = %d, want 303", code)
	}
}

// The account-wide rule list shows only apps the caller may still see: a
// creator removed from a company no longer reads its apps' IP lists, header
// values and WAF settings.
func TestEdgeRuleListHidesCompanyAppsAfterOffboarding(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator := *e.Store(), *e.Acct()
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "edge-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "edge-co", Name: "Edge Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "edge-company", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, Slug: "edge-personal", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{company, personal} {
		if _, err := store.CreateEdgeRule(ctx, challengeRule(creator.ID, app.ID)); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() map[string]bool {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/edge-rules", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
		}
		var rules []api.EdgeRuleResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &rules); err != nil {
			t.Fatal(err)
		}
		apps := map[string]bool{}
		for _, rule := range rules {
			apps[rule.AppID] = true
		}
		return apps
	}
	if got := listed(); !got[company.ID] || !got[personal.ID] {
		t.Fatalf("a member's list = %v, want both apps", got)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if got := listed(); got[company.ID] || !got[personal.ID] {
		t.Fatalf("an offboarded creator's list = %v, want only the personal app", got)
	}
}
