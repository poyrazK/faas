//go:build !no_pg

package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSharedAppPostgresMembershipAndResourceStorage(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := *e.Store()
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "pg-team-access", Name: "PG Team Access", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, (*e.Acct()).ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "pg-team-api"}, nil); rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	actor, headers := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	if rec := e.Do(t, "PUT", "/v1/apps/pg-team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, headers); rec.Code != 200 {
		t.Fatalf("shared env: %d %s", rec.Code, rec.Body)
	}
	app, err := store.AppBySlug(t.Context(), "pg-team-api")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAppEnvInScope(t.Context(), (*e.Acct()).ID, app.ID, "default")
	if err != nil || len(rows) != 1 || rows[0].Value != "debug" {
		t.Fatalf("resource env: %+v err=%v", rows, err)
	}
	if err := store.UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "PUT", "/v1/apps/pg-team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "error"}, headers); rec.Code != 403 {
		t.Fatalf("demoted member: %d %s", rec.Code, rec.Body)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "GET", "/v1/apps/pg-team-api", nil, headers); rec.Code != 401 {
		t.Fatalf("removed member: %d %s", rec.Code, rec.Body)
	}
}

func TestSharedAppPostgresCreatorCannotReadCompanyTraces(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "pg-company-traces", Name: "Company Traces", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "pg-company-trace-app", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if app.AccountID != creator.ID {
		t.Fatal("fixture must preserve the legacy creator namespace")
	}
	successor, _ := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, successor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	path := "/v1/account/traces/4bf92f3577b34da6a3ce929d0e0e4736/selected-apps?app_id=" + app.ID
	if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 404 {
		t.Fatalf("removed creator used account diagnostics for company app: %d %s", rec.Code, rec.Body)
	}
}
