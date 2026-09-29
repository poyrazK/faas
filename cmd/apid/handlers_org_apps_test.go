package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestDeveloperCanDeployWorkspaceAppAsMemberWithCreatorQuota(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedSharedOrgWithOwner(t, e, "org-deploy-app", "Org Deploy App", api.PlanPro)
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "workspace-deploy", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	developer, err := e.store.CreateAccount(ctx, "workspace-developer@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount developer: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, developer.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("AddOrgMember developer: %v", err)
	}
	plain, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(ctx, developer.ID, hash, "workspace-deployer", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey developer: %v", err)
	}
	developerEnv := e
	developerEnv.acct, developerEnv.key = developer, plain

	image := "registry.example.com/workspace@sha256:" + strings.Repeat("a", 64)
	rec := developerEnv.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps/"+app.Slug+"/deployments",
		api.CreateDeploymentRequest{Image: image}, map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("workspace deploy: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode deployment response: %v", err)
	}
	deployment, err := e.store.LatestDeployment(ctx, app.ID)
	if err != nil || deployment.ID != response.ID || deployment.DeployedByUserID != developer.ID {
		t.Fatalf("deployment = %+v, err=%v; want response id and actor %s", deployment, err, developer.ID)
	}
	ownerRate, err := e.store.ReadAccountDeployRate(ctx, e.acct.ID, e.acct.Plan.DeploysPerHour(), time.Now().UTC())
	if err != nil || ownerRate.Used != 1 {
		t.Fatalf("creator rate = %+v, err=%v; want one consumed deploy", ownerRate, err)
	}
	actorRate, err := e.store.ReadAccountDeployRate(ctx, developer.ID, developer.Plan.DeploysPerHour(), time.Now().UTC())
	if err != nil || actorRate.Used != 0 {
		t.Fatalf("developer rate = %+v, err=%v; want no creator quota charged", actorRate, err)
	}
	rows, err := e.store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(org.ID), Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Kind != "deploy.requested" || rows[0].ActorType != state.OrgActivityActorAPIKey || rows[0].ActorLabel != "workspace-deployer" {
		t.Fatalf("workspace activity = %+v, err=%v; want requested deployment by workspace-deployer key", rows, err)
	}
}

func TestWorkspaceDeploymentRejectsViewerAndBillingRoles(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedSharedOrgWithOwner(t, e, "org-deploy-roles", "Org Deploy Roles", api.PlanPro)
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "role-guard-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	for _, role := range []state.OrgRole{state.OrgRoleViewer, state.OrgRoleBilling} {
		t.Run(string(role), func(t *testing.T) {
			member, err := e.store.CreateAccount(ctx, "workspace-"+string(role)+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			if err := e.store.AddOrgMember(ctx, org.ID, member.ID, role, nil); err != nil {
				t.Fatalf("AddOrgMember: %v", err)
			}
			plain, hash, _ := api.GenerateAPIKey()
			if _, err := e.store.CreateAPIKey(ctx, member.ID, hash, "read-only", api.ScopesAdminOnly); err != nil {
				t.Fatalf("CreateAPIKey: %v", err)
			}
			memberEnv := e
			memberEnv.acct, memberEnv.key = member, plain
			rec := memberEnv.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps/"+app.Slug+"/deployments",
				api.CreateDeploymentRequest{Image: "registry.example.com/role-guard@sha256:" + strings.Repeat("b", 64)},
				map[string]string{"Content-Type": "application/json"})
			assertProblem(t, rec, http.StatusForbidden, api.CodeOrgRoleForbidden)
		})
	}
	if _, err := e.store.LatestDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("LatestDeployment after denied roles = %v, want no deployment", err)
	}
}

func TestWorkspaceDeploymentRequiresPathOrgAndPersistedOrgToMatch(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedSharedOrgWithOwner(t, e, "org-deploy-path", "Org Deploy Path", api.PlanPro)
	foreign, err := e.store.CreateOrg(ctx, state.Org{Slug: "org-deploy-foreign", Name: "Foreign", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg foreign: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, foreign.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember foreign owner: %v", err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: foreign.ID, Slug: "foreign-org-app", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp foreign: %v", err)
	}
	path := "/v1/orgs/" + org.Slug + "/apps/" + app.Slug + "/deployments"
	body := api.CreateDeploymentRequest{Image: "registry.example.com/foreign@sha256:" + strings.Repeat("c", 64)}
	for name, headers := range map[string]map[string]string{
		"persisted app org mismatch": {"Content-Type": "application/json"},
		"active org header mismatch": {"Content-Type": "application/json", "X-Active-Org": foreign.Slug},
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.do(t, http.MethodPost, path, body, headers)
			assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
		})
	}
	if _, err := e.store.LatestDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("LatestDeployment after org mismatch = %v, want no deployment", err)
	}
}

func TestListOrgAppsScopesToWorkspaceAndReturnsSafeSummary(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedSharedOrgWithOwner(t, e, "app-inventory-org", "App Inventory Org", api.PlanPro)
	viewer, err := e.store.CreateAccount(ctx, "app-inventory-viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount viewer: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatalf("AddOrgMember viewer: %v", err)
	}
	plain, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(ctx, viewer.ID, hash, "viewer-inventory", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey viewer: %v", err)
	}

	createdAt := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	for _, app := range []state.App{
		{AccountID: e.acct.ID, OrgID: org.ID, Slug: "owner-workspace-app", Type: state.AppTypeApp, Runtime: "node22", CreatedAt: createdAt},
		{AccountID: viewer.ID, OrgID: org.ID, Slug: "viewer-workspace-app", Type: state.AppTypeFunction, Runtime: "python313", CreatedAt: createdAt.Add(time.Minute)},
		{AccountID: e.acct.ID, OrgID: org.ID, Slug: "deleted-workspace-app", Status: state.AppDeleted, CreatedAt: createdAt.Add(2 * time.Minute)},
	} {
		if _, err := e.store.CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %q: %v", app.Slug, err)
		}
	}
	foreign, err := e.store.CreateOrg(ctx, state.Org{Slug: "app-inventory-foreign", Name: "Foreign", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg foreign: %v", err)
	}
	for _, app := range []state.App{
		{AccountID: e.acct.ID, OrgID: foreign.ID, Slug: "foreign-workspace-app"},
		{AccountID: e.acct.ID, Slug: "unattributed-app"},
	} {
		if _, err := e.store.CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %q: %v", app.Slug, err)
		}
	}

	viewerEnv := e
	viewerEnv.acct, viewerEnv.key = viewer, plain
	rec := viewerEnv.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/apps", nil, nil)
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
	for _, secret := range []string{e.acct.ID, viewer.ID, foreign.ID, "account_id", "environment", "unattributed-app", "foreign-workspace-app", "deleted-workspace-app"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("app inventory leaked or included %q: %s", secret, rec.Body.String())
		}
	}
}

func TestListOrgAppDeploymentsAllowsWorkspaceViewerAndReturnsSafePagedStatus(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedSharedOrgWithOwner(t, e, "deploy-history-org", "Deploy History Org", api.PlanPro)
	viewer, err := e.store.CreateAccount(ctx, "deploy-history-viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount viewer: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, viewer.ID, state.OrgRoleViewer, nil); err != nil {
		t.Fatalf("AddOrgMember viewer: %v", err)
	}
	plain, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(ctx, viewer.ID, hash, "deployment-reader", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey viewer: %v", err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "shared-deploy-history"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	createdAt := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	for _, dep := range []state.Deployment{
		{ID: strings.Repeat("a", 32), AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Revision: 1, CreatedAt: createdAt, OverrideEnv: json.RawMessage(`{"TOKEN":"private-deploy-value"}`), SourceURL: "private-source-url", Error: "private-error-detail"},
		{ID: strings.Repeat("b", 32), AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, Revision: 2, CreatedAt: createdAt.Add(time.Minute)},
	} {
		if _, err := e.store.CreateDeployment(ctx, dep); err != nil {
			t.Fatalf("CreateDeployment %q: %v", dep.ID, err)
		}
	}
	viewerEnv := e
	viewerEnv.acct, viewerEnv.key = viewer, plain
	path := "/v1/orgs/" + org.Slug + "/apps/" + app.Slug + "/deployments"
	first := viewerEnv.do(t, http.MethodGet, path+"?limit=1", nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("list workspace deployments: status=%d body=%s", first.Code, first.Body.String())
	}
	var page api.OrgAppDeploymentListResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode workspace deployment page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != strings.Repeat("b", 32) || page.Items[0].Revision != 2 || page.Items[0].Status != string(state.DeployPending) || page.NextBefore == "" {
		t.Fatalf("first deployment page = %+v, want newest status and older-page cursor", page)
	}
	if strings.Contains(first.Body.String(), "private-deploy-value") || strings.Contains(first.Body.String(), "private-source-url") || strings.Contains(first.Body.String(), "private-error-detail") || strings.Contains(first.Body.String(), "override_env") {
		t.Fatalf("workspace summary leaked creator-scoped deployment details: %s", first.Body.String())
	}
	second := viewerEnv.do(t, http.MethodGet, path+"?limit=1&before="+url.QueryEscape(page.NextBefore), nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("list older workspace deployments: status=%d body=%s", second.Code, second.Body.String())
	}
	page = api.OrgAppDeploymentListResponse{}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode older workspace deployment page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != strings.Repeat("a", 32) || page.Items[0].Status != string(state.DeployLive) || page.NextBefore != "" {
		t.Fatalf("older deployment page = %+v, want previous live revision", page)
	}
}

func TestListOrgAppDeploymentsHidesAppsOutsidePathWorkspace(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedSharedOrgWithOwner(t, e, "deploy-history-scope", "Deploy History Scope", api.PlanPro)
	foreign, err := e.store.CreateOrg(ctx, state.Org{Slug: "deploy-history-foreign", Name: "Foreign", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg foreign: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, foreign.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember foreign owner: %v", err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: foreign.ID, Slug: "foreign-deploy-history"})
	if err != nil {
		t.Fatalf("CreateApp foreign: %v", err)
	}
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive}); err != nil {
		t.Fatalf("CreateDeployment foreign: %v", err)
	}
	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/apps/"+app.Slug+"/deployments", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
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
