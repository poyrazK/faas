package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateOrgAppPersistsWorkspaceAttribution(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "app-create-org", "App Create Org", api.PlanPro)
	rec := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org app: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode app response: %v", err)
	}

	apps, err := (*e.Store()).ListApps(context.Background(), (*e.Acct()).ID)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	var created state.App
	for _, app := range apps {
		if app.Slug == "team-api" {
			created = app
			break
		}
	}
	if created.ID == "" || created.AccountID != (*e.Acct()).ID || created.OrgID != org.ID || response.ID != created.ID {
		t.Fatalf("created app = %+v response id=%q; want creator %q and workspace %q", created, response.ID, (*e.Acct()).ID, org.ID)
	}

	rows, err := (*e.Store()).ListOrgActivity(context.Background(), state.OrgActivityFilter{
		OrgID: uuid.MustParse(org.ID), Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListOrgActivity: %v", err)
	}
	appID := uuid.MustParse(created.ID)
	if len(rows) != 1 || rows[0].Kind != "app.created" || rows[0].AppID == nil || *rows[0].AppID != appID {
		t.Fatalf("workspace activity = %+v, want app.created for %s", rows, created.ID)
	}
}

func TestCreateOrgAppRejectsViewer(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "app-create-viewer", "App Create Viewer", api.PlanPro)
	viewer, err := (*e.Store()).CreateAccount(context.Background(), "viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount viewer: %v", err)
	}
	if err := (*e.Store()).AddOrgMember(context.Background(), org.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatalf("AddOrgMember viewer: %v", err)
	}
	plain, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), viewer.ID, hash, "viewer-test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey viewer: %v", err)
	}
	viewerEnv := e
	(*viewerEnv.Acct()) = viewer
	(*viewerEnv.Key()) = plain

	rec := viewerEnv.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "forbidden-app"}, nil)
	apidserver.AssertProblem(t, rec, http.StatusForbidden, api.CodeOrgRoleForbidden)
	apps, err := (*e.Store()).ListApps(context.Background(), viewer.ID)
	if err != nil {
		t.Fatalf("ListApps viewer: %v", err)
	}
	if len(apps) != 0 {
		t.Fatalf("viewer created apps: %+v", apps)
	}
}

func TestListOrgAppsScopesToWorkspaceAndReturnsSafeSummary(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "app-inventory-org", "App Inventory Org", api.PlanPro)
	viewer, err := (*e.Store()).CreateAccount(ctx, "app-inventory-viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount viewer: %v", err)
	}
	if err := (*e.Store()).AddOrgMember(ctx, org.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatalf("AddOrgMember viewer: %v", err)
	}
	plain, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(ctx, viewer.ID, hash, "viewer-inventory", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey viewer: %v", err)
	}

	createdAt := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	for _, app := range []state.App{
		{AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: "owner-workspace-app", Type: state.AppTypeApp, Runtime: "node22", CreatedAt: createdAt},
		{AccountID: viewer.ID, OrgID: org.ID, Slug: "viewer-workspace-app", Type: state.AppTypeFunction, Runtime: "python313", CreatedAt: createdAt.Add(time.Minute)},
		{AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: "deleted-workspace-app", Status: state.AppDeleted, CreatedAt: createdAt.Add(2 * time.Minute)},
	} {
		if _, err := (*e.Store()).CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %q: %v", app.Slug, err)
		}
	}
	foreign, err := (*e.Store()).CreateOrg(ctx, state.Org{Slug: "app-inventory-foreign", Name: "Foreign", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg foreign: %v", err)
	}
	for _, app := range []state.App{
		{AccountID: (*e.Acct()).ID, OrgID: foreign.ID, Slug: "foreign-workspace-app"},
		{AccountID: (*e.Acct()).ID, Slug: "unattributed-app"},
	} {
		if _, err := (*e.Store()).CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %q: %v", app.Slug, err)
		}
	}

	viewerEnv := e
	(*viewerEnv.Acct()), (*viewerEnv.Key()) = viewer, plain
	rec := viewerEnv.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/apps", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list org apps: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.OrgAppListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode app inventory: %v", err)
	}
	if len(response.Apps) != 2 || response.Apps[0].Slug != "viewer-workspace-app" || response.Apps[1].Slug != "owner-workspace-app" {
		t.Fatalf("app inventory = %#v, want the two active apps in newest-first order", response.Apps)
	}
	if response.Apps[0].Type != string(state.AppTypeFunction) || response.Apps[0].Runtime != "python313" || response.Apps[0].Status != string(state.AppActive) {
		t.Fatalf("app summary = %#v, want type/runtime/status fields", response.Apps[0])
	}
	for _, secret := range []string{(*e.Acct()).ID, viewer.ID, foreign.ID, "account_id", "environment", "unattributed-app", "foreign-workspace-app", "deleted-workspace-app"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("app inventory leaked or included %q: %s", secret, rec.Body.String())
		}
	}
}

func TestCreateAppIgnoresActiveOrgHintWithoutOrgRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	personal := apidserver.SeedActivityPersonalOrg(t, e)
	shared := apidserver.SeedSharedOrgWithOwner(t, e, "app-create-explicit", "Explicit App Workspace", api.PlanPro)
	rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "personal-api"}, map[string]string{
		"X-Active-Org": shared.Slug,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account app: status=%d body=%s", rec.Code, rec.Body.String())
	}
	apps, err := (*e.Store()).ListApps(context.Background(), (*e.Acct()).ID)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("ListApps returned %d apps, want 1", len(apps))
	}
	if apps[0].OrgID != personal.ID || apps[0].OrgID == shared.ID {
		t.Fatalf("account-scoped app org_id = %q; want personal %q, not active-org hint %q", apps[0].OrgID, personal.ID, shared.ID)
	}
}
