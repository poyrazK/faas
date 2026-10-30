package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// The dashboard's app pages compared the app's creator with the signed-in
// account, so a company app answered "no such app" to every member but its
// creator, even one whose role allowed the page, and only the API worked
// (bug hunt 2026-10-28 B36). A member who did not create the app now reads
// its pages and, with a role that may, parks it from the dashboard; a viewer
// reads but cannot act.
func TestDashboardCompanyAppServesItsMembers(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	ctx := t.Context()
	creator, err := env.store.CreateAccount(ctx, "acme-creator@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := env.store.CreateAccount(ctx, "acme-viewer@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := env.store.CreateOrg(ctx, state.Org{Slug: "acme-dash", Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, env.account.ID: state.OrgRoleAdmin, viewer.ID: state.OrgRoleViewer} {
		if err := env.store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "acme-dash-api",
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	viewerToken, err := apidserver.MintDashboardSession(ctx, env.store, env.sessions, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	viewerSession := &http.Cookie{Name: apidserver.SessionCookie, Value: viewerToken}

	for _, page := range []string{"/dashboard/apps/acme-dash-api", "/dashboard/apps/acme-dash-api/logs", "/dashboard/apps/acme-dash-api/instances"} {
		for name, session := range map[string]*http.Cookie{"admin": env.session, "viewer": viewerSession} {
			if got := dashboardGET(t, env.h, session, page); got.Code != http.StatusOK {
				t.Fatalf("%s GET %s = %d, want 200", name, page, got.Code)
			}
		}
	}
	park := func(acct state.Account, session *http.Cookie) int {
		t.Helper()
		token, err := middleware.IssueForAuthenticatedNamed(env.sessions, apidserver.DashboardInstanceAction, acct.ID, apidserver.DashboardInstanceCSRFCookie)
		if err != nil {
			t.Fatal(err)
		}
		return apidserver.DashboardPOST(t, env.h, session, "/dashboard/apps/acme-dash-api/instances/park",
			map[string]string{middleware.FormFieldName: token},
			&http.Cookie{Name: apidserver.DashboardInstanceCSRFCookie, Value: token}).Code
	}
	if code := park(viewer, viewerSession); code != http.StatusForbidden {
		t.Fatalf("viewer park = %d, want 403", code)
	}
	if code := park(env.account, env.session); code != http.StatusSeeOther {
		t.Fatalf("admin park = %d, want 303", code)
	}
	app, err := env.store.AppBySlug(ctx, "acme-dash-api")
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != state.AppEvictedCold {
		t.Fatalf("app status after the admin's park = %q, want %q", app.Status, state.AppEvictedCold)
	}
}
