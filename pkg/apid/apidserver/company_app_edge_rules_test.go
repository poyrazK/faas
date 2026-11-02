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

// adr: 091 — amendment 2026-10-31 (round 7): the edge-rule routes had no
// reviewed organization permission, so a company app's rules answered only
// its creator: no other owner or admin could change them or switch on attack
// mode, and once the creator left nobody could. Members who can read the app
// now read its rules; owners and admins change them.
func TestCompanyAppEdgeRulesFollowOrganizationRoles(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "edge-team", "Edge Team", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "edge-team-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asDeveloper := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	rule := api.CreateEdgeRuleRequest{MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Kind: string(state.EdgeRuleKindHeaders),
		Action: json.RawMessage(`{"response_headers":[{"name":"X-Team","value":"1","action":"set"}]}`)}

	created := e.Do(t, http.MethodPost, "/v1/apps/edge-team-api/edge-rules", rule, asAdmin)
	if created.Code != http.StatusCreated {
		t.Fatalf("admin creating a company app's rule = %d %s, want 201", created.Code, created.Body)
	}
	var row api.EdgeRuleResponse
	if err := json.Unmarshal(created.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	disabled := false
	for _, tc := range []struct {
		name, method, path string
		body               any
		headers            map[string]string
		want               int
	}{
		{"viewer lists", http.MethodGet, "/v1/apps/edge-team-api/edge-rules", nil, asViewer, http.StatusOK},
		{"viewer reads", http.MethodGet, "/v1/edge-rules/" + row.ID, nil, asViewer, http.StatusOK},
		{"viewer history", http.MethodGet, "/v1/apps/edge-team-api/edge-rules/versions", nil, asViewer, http.StatusOK},
		{"viewer cannot create", http.MethodPost, "/v1/apps/edge-team-api/edge-rules", rule, asViewer, http.StatusForbidden},
		{"developer cannot create", http.MethodPost, "/v1/apps/edge-team-api/edge-rules", rule, asDeveloper, http.StatusForbidden},
		{"developer cannot change", http.MethodPatch, "/v1/edge-rules/" + row.ID, api.UpdateEdgeRuleRequest{Enabled: &disabled}, asDeveloper, http.StatusForbidden},
		{"admin changes", http.MethodPatch, "/v1/edge-rules/" + row.ID, api.UpdateEdgeRuleRequest{Enabled: &disabled}, asAdmin, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := e.Do(t, tc.method, tc.path, tc.body, tc.headers); r.Code != tc.want {
				t.Fatalf("status = %d %s; want %d", r.Code, r.Body, tc.want)
			}
		})
	}

	// The creator leaves; the company still manages the app's rules.
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("remove creator = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/edge-rules/"+row.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("admin deleting the departed creator's rule = %d %s", r.Code, r.Body)
	}
}

// The dashboard's edge-rule forms take the same permissions.
func TestDashboardCompanyAppEdgeRuleFormsFollowOrganizationRoles(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	ctx := t.Context()
	creator, err := env.store.CreateAccount(ctx, "edge-creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := env.store.CreateAccount(ctx, "edge-viewer@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := env.store.CreateOrg(ctx, state.Org{Slug: "edge-dash", Name: "Edge", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, env.account.ID: state.OrgRoleAdmin, viewer.ID: state.OrgRoleViewer} {
		if err := env.store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	app, err := env.store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "edge-dash-api",
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := env.store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: creator.ID, AppID: app.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Team", Value: "1", Action: "set"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := apidserver.MintDashboardSession(ctx, env.store, env.sessions, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	toggle := func(acct state.Account, session *http.Cookie) int {
		t.Helper()
		token, err := middleware.IssueForAuthenticatedNamed(env.sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
		if err != nil {
			t.Fatal(err)
		}
		return apidserver.DashboardPOST(t, env.h, session, "/dashboard/apps/edge-dash-api/edge-rules/"+rule.ID+"/toggle",
			map[string]string{middleware.FormFieldName: token},
			&http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token}).Code
	}
	if code := toggle(viewer, &http.Cookie{Name: apidserver.SessionCookie, Value: viewerToken}); code != http.StatusForbidden {
		t.Fatalf("viewer toggle = %d, want 403", code)
	}
	if code := toggle(env.account, env.session); code != http.StatusSeeOther {
		t.Fatalf("admin toggle = %d, want 303", code)
	}
	got, err := env.store.GetEdgeRuleByID(ctx, rule.ID)
	if err != nil || got.Enabled {
		t.Fatalf("rule after the admin's toggle: enabled=%v err=%v, want disabled", got.Enabled, err)
	}
}
