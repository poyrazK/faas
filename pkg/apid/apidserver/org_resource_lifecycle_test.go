package apidserver_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgResourceLifecycleHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	orgResourceLifecycleHTTP(t, *e.Store(), *e.Acct(), e.Do)
}

func orgResourceLifecycleHTTP(t *testing.T, store state.Store, creator state.Account, do func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder) {
	t.Helper()
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "owned-company", Name: "Owned Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_http_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_http_company"); err != nil {
		t.Fatal(err)
	}
	created := do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "owned-company-app"}, nil)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	app, err := store.AppBySlug(ctx, "owned-company-app")
	if err != nil {
		t.Fatal(err)
	}
	next, headers := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/apps/" + app.Slug, nil},
		{"PUT", "/v1/apps/" + app.Slug + "/env/REGION", api.PutAppEnvRequest{Value: "eu"}},
		{"GET", "/v1/orgs/" + org.Slug + "/billing/status", nil},
		{"GET", "/v1/orgs/" + org.Slug + "/invoices", nil},
	} {
		rec := do(t, request.method, request.path, request.body, headers)
		if rec.Code != 200 {
			t.Fatalf("%s %s: %d %s", request.method, request.path, rec.Code, rec.Body)
		}
	}
	minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	if err := store.AppendUsage(ctx, app.AccountID, app.ID, uuid.NewString(), minute, 3600, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	usage := do(t, "GET", "/v1/orgs/"+org.Slug+"/usage?month="+minute.Format("2006-01"), nil, headers)
	var rows []api.UsageResponse
	if usage.Code != 200 {
		t.Fatalf("company usage: %d %s", usage.Code, usage.Body)
	}
	if err := json.Unmarshal(usage.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].AppID != app.ID {
		t.Fatalf("company usage rows: %+v %v", rows, err)
	}
	if rec := do(t, "GET", "/v1/apps/"+app.Slug, nil, nil); rec.Code != 401 {
		t.Fatalf("erased creator authenticated: %d %s", rec.Code, rec.Body)
	}
	// New colleagues receive company runtime entitlements at creation.
	streaming := true
	rec := do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "next-company-app", StreamingEnabled: &streaming}, headers)
	if rec.Code != 201 {
		t.Fatalf("new member's company app: %d %s", rec.Code, rec.Body)
	}
}

func TestOrgBillingRoleIsolation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "billing-role-company", "Billing Role Company", api.PlanPro)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billing := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	for _, tc := range []struct {
		path    string
		headers map[string]string
		status  int
	}{
		{"/v1/orgs/" + org.Slug + "/billing/status", viewer, 200},
		{"/v1/orgs/" + org.Slug + "/usage", viewer, 200},
		{"/v1/orgs/" + org.Slug + "/invoices", viewer, 403},
		{"/v1/orgs/" + org.Slug + "/invoices", billing, 200},
		{"/v1/orgs/" + org.Slug + "/billing/portal", billing, 403},
	} {
		rec := e.Do(t, "GET", tc.path, nil, tc.headers)
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
	foreign, err := store.CreateOrg(t.Context(), state.Org{Slug: "foreign-billing-company", Name: "Foreign", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, "GET", "/v1/orgs/"+foreign.Slug+"/usage", nil, viewer)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("foreign organization usage exposed: %d %s", rec.Code, rec.Body)
	}
}

func TestOrgBillingPlanUsesCompanySubject(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithBillingPortalURL("https://billing.example.com/portal?account={account_id}")
	org := apidserver.SeedSharedOrgWithOwner(t, e, "company-plan", "Company", api.PlanFree)
	rec := e.Do(t, "PATCH", "/v1/orgs/"+org.Slug+"/plan", map[string]string{"plan": "free"}, nil)
	var current api.OrgPlanResponse
	if rec.Code != 200 {
		t.Fatalf("current company plan: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &current); err != nil || current.OrgID != org.ID || current.Plan != api.PlanFree {
		t.Fatalf("company plan response: %+v %v", current, err)
	}
	rec = e.Do(t, "PATCH", "/v1/orgs/"+org.Slug+"/plan", map[string]string{"plan": "pro"}, nil)
	var problem api.Problem
	if rec.Code != 402 {
		t.Fatalf("unconfirmed company upgrade: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || !strings.Contains(problem.BillingPortalURL, org.ID) {
		t.Fatalf("company portal used another billing subject: %+v %v", problem, err)
	}
	if saved, err := (*e.Store()).OrgByID(t.Context(), org.ID); err != nil || saved.Plan != api.PlanFree {
		t.Fatalf("unconfirmed provider upgrade granted company entitlements: %+v %v", saved, err)
	}
	if actor, err := (*e.Store()).AccountByID(t.Context(), e.Acct().ID); err != nil || actor.Plan != api.PlanPro {
		t.Fatalf("company billing changed the human account: %+v %v", actor, err)
	}
}

func TestPersonalExportExcludesCompanyResourcesAfterRemoval(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	actor := *e.Acct()
	owner, err := store.CreateAccount(ctx, "export-company-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "export-company", Name: "Export Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, actor.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: actor.ID, OrgID: org.ID, Slug: "private-company-app"})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: actor.ID, Slug: "export-personal-app"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: company.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecret(ctx, actor.ID, company.ID, "COMPANY_TOKEN", []byte("new-company-ciphertext")); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"app_id": company.ID, "detail": "new-private-company-detail"})
	if err := store.AppendEvent(ctx, "apid", "company.updated", &actor.ID, data); err != nil {
		t.Fatal(err)
	}
	response := e.Do(t, "GET", "/v1/account/export", nil, nil)
	if response.Code != 200 {
		t.Fatalf("personal export: %d %s", response.Code, response.Body)
	}
	for _, private := range []string{company.ID, company.Slug, "COMPANY_TOKEN", "new-private-company-detail"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("personal export disclosed company resource %q", private)
		}
	}
	if !strings.Contains(response.Body.String(), personal.Slug) {
		t.Fatal("personal export lost personal application")
	}
}
