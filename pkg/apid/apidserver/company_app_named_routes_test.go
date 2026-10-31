package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Custom domains and crons name a company app in the request body or by their
// own ID. Only the app's creator could reach them, priced by the creator's
// personal plan: a company's admins could not remove a production domain or
// stop a cron on an app another member created, and a Free member of a Pro
// company was refused crons the company pays for (bug hunt 2026-10-28 B9,
// H90).
//
// adr: 20261026151103
func TestCompanyMembersManageDomainsAndCronsOnAnotherMembersApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := *e.Acct()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "named-routes", Name: "Named Routes", Plan: api.PlanPro, ResourceOwnershipEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "company-web"})
	if err != nil {
		t.Fatal(err)
	}
	// sharedAppMember accounts are on the Free plan.
	_, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)

	const domain = "www.named-routes.example"
	if r := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{AppID: app.Slug, Domain: domain}, nil); r.Code != http.StatusAccepted {
		t.Fatalf("creator adds a domain = %d %s", r.Code, r.Body)
	}
	cronReq := api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/"}
	if r := e.Do(t, http.MethodPost, "/v1/crons", cronReq, asViewer); r.Code != http.StatusForbidden {
		t.Fatalf("viewer adds a cron = %d %s", r.Code, r.Body)
	}
	r := e.Do(t, http.MethodPost, "/v1/crons", cronReq, asAdmin)
	if r.Code != http.StatusCreated {
		t.Fatalf("Free admin adds a cron to the Pro company's app = %d %s", r.Code, r.Body)
	}
	var cron api.CronResponse
	if err := json.Unmarshal(r.Body.Bytes(), &cron); err != nil || cron.ID == "" {
		t.Fatalf("cron response %s: %v", r.Body, err)
	}

	for _, c := range []struct {
		method, path string
		headers      map[string]string
		want         int
	}{
		{http.MethodGet, "/v1/crons/" + cron.ID, asViewer, http.StatusOK},
		{http.MethodGet, "/v1/domains/" + domain, asViewer, http.StatusOK},
		{http.MethodPost, "/v1/crons/" + cron.ID + "/run", asViewer, http.StatusForbidden},
		{http.MethodDelete, "/v1/domains/" + domain, asViewer, http.StatusForbidden},
		{http.MethodPost, "/v1/crons/" + cron.ID + "/run", asAdmin, http.StatusAccepted},
		{http.MethodDelete, "/v1/domains/" + domain, asAdmin, http.StatusNoContent},
		{http.MethodDelete, "/v1/crons/" + cron.ID, asAdmin, http.StatusNoContent},
	} {
		if r := e.Do(t, c.method, c.path, nil, c.headers); r.Code != c.want {
			t.Fatalf("%s %s = %d, want %d: %s", c.method, c.path, r.Code, c.want, r.Body)
		}
	}
}

// The company's plan also refuses: the Pro creator of a Free company's app
// gets no crons on it.
//
// adr: 20261026151103
func TestCompanyAppCronsFollowTheCompanysPlan(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := *e.Acct()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "free-co", Name: "Free Co", Plan: api.PlanFree, ResourceOwnershipEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "free-co-web"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/crons", api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/"}, nil)
	if r.Code != http.StatusPaymentRequired {
		t.Fatalf("Pro creator adds a cron to a Free company's app = %d %s, want 402", r.Code, r.Body)
	}
}
