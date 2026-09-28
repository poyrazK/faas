package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateOrgAppPersistsWorkspaceAttribution(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "app-create-org", "App Create Org", api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org app: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode app response: %v", err)
	}

	apps, err := e.store.ListApps(context.Background(), e.acct.ID)
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
	if created.ID == "" || created.AccountID != e.acct.ID || created.OrgID != org.ID || response.ID != created.ID {
		t.Fatalf("created app = %+v response id=%q; want creator %q and workspace %q", created, response.ID, e.acct.ID, org.ID)
	}

	rows, err := e.store.ListOrgActivity(context.Background(), state.OrgActivityFilter{
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
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "app-create-viewer", "App Create Viewer", api.PlanPro)
	viewer, err := e.store.CreateAccount(context.Background(), "viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount viewer: %v", err)
	}
	if err := e.store.AddOrgMember(context.Background(), org.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatalf("AddOrgMember viewer: %v", err)
	}
	plain, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), viewer.ID, hash, "viewer-test", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey viewer: %v", err)
	}
	viewerEnv := e
	viewerEnv.acct = viewer
	viewerEnv.key = plain

	rec := viewerEnv.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "forbidden-app"}, nil)
	assertProblem(t, rec, http.StatusForbidden, api.CodeOrgRoleForbidden)
	apps, err := e.store.ListApps(context.Background(), viewer.ID)
	if err != nil {
		t.Fatalf("ListApps viewer: %v", err)
	}
	if len(apps) != 0 {
		t.Fatalf("viewer created apps: %+v", apps)
	}
}

func TestCreateAppIgnoresActiveOrgHintWithoutOrgRoute(t *testing.T) {
	e := setup(t, api.PlanPro)
	personal := seedActivityPersonalOrg(t, e)
	shared := seedSharedOrgWithOwner(t, e, "app-create-explicit", "Explicit App Workspace", api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "personal-api"}, map[string]string{
		"X-Active-Org": shared.Slug,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account app: status=%d body=%s", rec.Code, rec.Body.String())
	}
	apps, err := e.store.ListApps(context.Background(), e.acct.ID)
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
