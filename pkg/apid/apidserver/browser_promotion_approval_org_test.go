package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company project's agent asks for a browser review of a protected
// promotion (ADR-20261112230000). Only a member who may approve protected
// environments can approve it, only the member who asked can exchange it, and
// the approval records the approving member.
func TestMCPBrowserPromotionApprovalForCompanyProject(t *testing.T) {
	srv, store := newCliAuthTestServer(t)
	creator, err := store.CreateAccount(t.Context(), "company-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "review-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, OrgID: org.ID, ProjectID: project.ID, Slug: "company-shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: creator.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "production"} {
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope, SourceSHA256: scope, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(t.Context(), deployment.ID, "/rootfs/"+scope, "apps/"+scope+".ext4", 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	plan, problem := srv.apid.buildProjectEnvironmentPromotionPlan(t.Context(), creator, "shop", "staging", "production", false)
	if problem != nil {
		t.Fatal(problem)
	}
	member := func(email string, role state.OrgRole) (state.Account, string, string) {
		acct, err := store.CreateAccount(t.Context(), email, api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(t.Context(), org.ID, acct.ID, role, nil); err != nil {
			t.Fatal(err)
		}
		plain, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateOrgAPIKey(t.Context(), org.ID, acct.ID, hash, "agent-"+string(role), []string{api.ScopeAppsRead, api.ScopeDeployWrite}, nil); err != nil {
			t.Fatal(err)
		}
		return acct, plain, srv.issueBrowserSessionForTest(t, email)
	}
	_, developerKey, developerSession := member("developer@team.example", state.OrgRoleDeveloper)
	admin, adminKey, adminSession := member("admin@team.example", state.OrgRoleAdmin)

	call := func(key, path string, input any) *httptest.ResponseRecorder {
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/v1/orgs/"+org.Slug+"/projects/shop/environments/production/"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	requested := call(developerKey, "approval-requests", api.BrowserPromotionApprovalRequest{PromotionToken: plan.Preview.PromotionToken, Reason: "ship the company release"})
	if requested.Code != 201 {
		t.Fatalf("developer's agent request = %d %s", requested.Code, requested.Body)
	}
	var request api.BrowserPromotionApprovalResponse
	if err := json.Unmarshal(requested.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	f := browserPromotionFixture{server: srv, account: creator, session: adminSession, preview: plan.Preview}

	// A developer may not approve a protected promotion, even its own agent's.
	page := httptest.NewRequest("GET", request.ReviewURL, nil)
	page.AddCookie(&http.Cookie{Name: sessionCookie, Value: developerSession})
	rendered := httptest.NewRecorder()
	srv.ServeHTTP(rendered, page)
	if strings.Contains(rendered.Body.String(), plan.Preview.PromotionHash) || !strings.Contains(rendered.Body.String(), "unavailable for the signed-in account") {
		t.Fatalf("developer review page = %d %s; want refused", rendered.Code, rendered.Body)
	}

	cookie, csrf := f.reviewPage(t, request)
	if approved := f.submit(request, cookie, csrf, adminSession); approved.Code != 200 {
		t.Fatalf("admin review = %d %s", approved.Code, approved.Body)
	}
	exchange := api.BrowserPromotionApprovalExchange{Ticket: request.Ticket, ExchangeCode: request.ExchangeCode}
	if other := call(adminKey, "approval-requests/exchange", exchange); other.Code != http.StatusGone {
		t.Fatalf("exchange by a member who did not ask = %d %s; want 410", other.Code, other.Body)
	}
	exchanged := call(developerKey, "approval-requests/exchange", exchange)
	if exchanged.Code != 200 {
		t.Fatalf("exchange by the requesting member = %d %s", exchanged.Code, exchanged.Body)
	}
	var approval api.ProjectEnvironmentApprovalResponse
	if err := json.Unmarshal(exchanged.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	stored, err := store.ProjectEnvironmentApprovalByID(t.Context(), creator.ID, "shop", "production", approval.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ApprovedByAccountID != admin.ID {
		t.Fatalf("approval recorded approver %q; want the admin %q", stored.ApprovedByAccountID, admin.ID)
	}

	// A reviewer who loses the approve role before the exchange voids it.
	second := call(developerKey, "approval-requests", api.BrowserPromotionApprovalRequest{PromotionToken: plan.Preview.PromotionToken, Reason: "again"})
	if second.Code != 201 {
		t.Fatalf("second request = %d %s", second.Code, second.Body)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	cookie, csrf = f.reviewPage(t, request)
	if approved := f.submit(request, cookie, csrf, adminSession); approved.Code != 200 {
		t.Fatalf("second admin review = %d", approved.Code)
	}
	if err := store.UpdateOrgMemberRole(t.Context(), org.ID, admin.ID, state.OrgRoleDeveloper); err != nil {
		t.Fatal(err)
	}
	if demoted := call(developerKey, "approval-requests/exchange", api.BrowserPromotionApprovalExchange{Ticket: request.Ticket, ExchangeCode: request.ExchangeCode}); demoted.Code != http.StatusGone {
		t.Fatalf("exchange after the reviewer lost the approve role = %d %s; want 410", demoted.Code, demoted.Body)
	}
}
