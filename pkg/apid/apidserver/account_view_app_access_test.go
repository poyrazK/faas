package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Dashboard widgets and incidents name an app outside their URL, so
// appaccess.Wrap never decides for them. They apply the account inventories'
// rule themselves: a creator who left a company no longer reads the
// company app's metrics, log counts or incidents through an account view.
func TestAccountViewsHideCompanyAppsAfterOffboarding(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, srv, creator := *e.Store(), *e.S(), *e.Acct()
	ctx := t.Context()
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
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "acme-api", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	view := middleware.WithPrincipal(ctx, creator, nil, nil)
	if _, ok := srv.DashboardWidgetAppForTest(view, creator, app.Slug); !ok {
		t.Fatal("a member's dashboard widget cannot read the company app")
	}
	if !srv.IncidentAppVisibleForTest(view, creator, app) {
		t.Fatal("a member cannot read the company app's incidents")
	}

	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.DashboardWidgetAppForTest(view, creator, app.Slug); ok {
		t.Fatal("an offboarded creator's dashboard widget still reads the company app")
	}
	if srv.IncidentAppVisibleForTest(view, creator, app) {
		t.Fatal("an offboarded creator still reads the company app's incidents")
	}
}
