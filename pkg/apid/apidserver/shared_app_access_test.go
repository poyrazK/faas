package apidserver_test

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func sharedAppMember(t *testing.T, store state.Store, org state.Org, role state.OrgRole, scopes []string) (state.Account, map[string]string) {
	t.Helper()
	actor, err := store.CreateAccount(t.Context(), string(role)+"@team.example", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, actor.ID, role, nil); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(t.Context(), org.ID, actor.ID, hash, "team-"+string(role), scopes, nil); err != nil {
		t.Fatal(err)
	}
	return actor, map[string]string{"Authorization": "Bearer " + plain}
}

func TestSharedAppTeamOperationsAndRoleRestrictions(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-access", "Team Access", api.PlanPro)
	created := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	store := *e.Store()
	_, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billing := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	for _, tc := range []struct {
		name, method, path string
		body               any
		headers            map[string]string
		want               int
	}{
		{"developer detail", "GET", "/v1/apps/team-api", nil, developer, 200},
		{"viewer detail", "GET", "/v1/apps/team-api", nil, viewer, 200},
		{"billing no detail", "GET", "/v1/apps/team-api", nil, billing, 403},
		{"developer configuration", "PUT", "/v1/apps/team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, developer, 200},
		{"viewer configuration read", "GET", "/v1/apps/team-api/env", nil, viewer, 200},
		{"viewer configuration denial", "PUT", "/v1/apps/team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "error"}, viewer, 403},
		{"developer security denial", "PATCH", "/v1/apps/team-api/security", map[string]string{"security_policy": "off"}, developer, 403},
		{"developer delete denial", "DELETE", "/v1/apps/team-api", nil, developer, 403},
		{"viewer deploy denial", "POST", "/v1/apps/team-api/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("a", 64)}, viewer, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.Do(t, tc.method, tc.path, tc.body, tc.headers)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s; want %d", rec.Code, rec.Body, tc.want)
			}
		})
	}
	app, err := store.AppBySlug(t.Context(), "team-api")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListAppEnvInScope(t.Context(), (*e.Acct()).ID, app.ID, "default")
	if err != nil || len(rows) != 1 || rows[0].Value != "debug" {
		t.Fatalf("creator-owned env = %+v, error=%v", rows, err)
	}
	defer apidserver.WithTestRecipient(t)()
	secret := e.Do(t, "PUT", "/v1/apps/team-api/secrets/TOKEN", map[string]string{"value": "team-secret"}, developer)
	if secret.Code != 200 {
		t.Fatalf("developer secret: %d %s", secret.Code, secret.Body)
	}
	metadata := e.Do(t, "GET", "/v1/apps/team-api/secrets", nil, viewer)
	if metadata.Code != 200 || strings.Contains(metadata.Body.String(), "team-secret") {
		t.Fatalf("secret metadata: %d %s", metadata.Code, metadata.Body)
	}
	_, admin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if rec := e.Do(t, "DELETE", "/v1/apps/team-api", nil, admin); rec.Code != 204 {
		t.Fatalf("admin delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "POST", "/v1/apps/team-api/restore", nil, viewer); rec.Code != 403 {
		t.Fatalf("viewer restore: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "POST", "/v1/apps/team-api/restore", nil, admin); rec.Code != 200 {
		t.Fatalf("admin restore: %d %s", rec.Code, rec.Body)
	}
}

func TestSharedAppDeployUsesResourceLimitsAndRealActor(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-deploy", "Team Deploy", api.PlanPro)
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil); rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	actor, headers := sharedAppMember(t, *e.Store(), org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	headers["Idempotency-Key"] = "team-deploy"
	deploy := e.Do(t, "POST", "/v1/apps/team-api/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("a", 64)}, headers)
	if deploy.Code != 202 {
		t.Fatalf("deploy: %d %s", deploy.Code, deploy.Body)
	}
	var response api.DeploymentResponse
	if err := json.Unmarshal(deploy.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	stored, err := (*e.Store()).DeploymentByID(t.Context(), response.ID)
	if err != nil || stored.DeployedByUserID != actor.ID {
		t.Fatalf("deployment actor=%q want %q, err=%v", stored.DeployedByUserID, actor.ID, err)
	}
	secondActor, secondHeaders := sharedAppMember(t, *e.Store(), org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	secondHeaders["Idempotency-Key"] = headers["Idempotency-Key"]
	second := e.Do(t, "POST", "/v1/apps/team-api/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("b", 64)}, secondHeaders)
	if second.Code != 202 {
		t.Fatalf("second member deploy: %d %s", second.Code, second.Body)
	}
	var secondResponse api.DeploymentResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatal(err)
	}
	secondStored, err := (*e.Store()).DeploymentByID(t.Context(), secondResponse.ID)
	if err != nil || secondStored.ID == stored.ID || secondStored.DeployedByUserID != secondActor.ID {
		t.Fatalf("another member replayed the first receipt: %+v err=%v", secondStored, err)
	}
	for _, path := range []string{"/v1/deployments/" + response.ID, "/v1/apps/team-api/deployments/" + response.ID + "/summary", "/v1/apps/team-api/deployments/latest"} {
		if rec := e.Do(t, "GET", path, nil, headers); rec.Code != 200 {
			t.Fatalf("read %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	foreignOrg := apidserver.SeedSharedOrgWithOwner(t, e, "foreign-deploy", "Foreign Deploy", api.PlanPro)
	foreignApp, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, OrgID: foreignOrg.ID, Slug: "foreign-api", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	foreignDeployment, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: foreignApp.ID, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	foreignCancel := e.Do(t, "POST", "/v1/apps/team-api/deployments/"+foreignDeployment.ID+"/cancel", nil, headers)
	if foreignCancel.Code != 404 {
		t.Fatalf("cross-app cancellation: %d %s", foreignCancel.Code, foreignCancel.Body)
	}
	cancel := e.Do(t, "POST", "/v1/apps/team-api/deployments/"+secondResponse.ID+"/cancel", nil, headers)
	if cancel.Code != 200 {
		t.Fatalf("team cancellation: %d %s", cancel.Code, cancel.Body)
	}
	cancelled, err := (*e.Store()).DeploymentByID(t.Context(), secondResponse.ID)
	if err != nil || cancelled.CancelledByPrincipal != actor.ID {
		t.Fatalf("cancellation actor: %+v err=%v", cancelled, err)
	}
	if err := (*e.Store()).UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	replay := e.Do(t, "POST", "/v1/apps/team-api/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("a", 64)}, headers)
	if replay.Code != 403 {
		t.Fatalf("demoted actor replay: %d %s", replay.Code, replay.Body)
	}
}

func TestSharedAppSessionActivityKeepsActualActor(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-session", "Team Session", api.PlanPro)
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	actor, _ := sharedAppMember(t, *e.Store(), org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	memberEnv := e
	*memberEnv.Acct() = actor
	if rec := memberEnv.DoAdmin(t, "PUT", "/v1/apps/team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, nil); rec.Code != 200 {
		t.Fatalf("session write: %d %s", rec.Code, rec.Body)
	}
	rows, err := (*e.Store()).ListOrgActivity(t.Context(), state.OrgActivityFilter{OrgID: uuid.MustParse(org.ID), Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind == "env.set" {
			if row.ActorAccountID == nil || *row.ActorAccountID != uuid.MustParse(actor.ID) {
				t.Fatalf("session attributed to resource owner: %+v", row)
			}
			return
		}
	}
	t.Fatal("missing env.set activity")
}

func TestSharedAppKeyScopesAndTenantBoundaries(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-scopes", "Team Scopes", api.PlanPro)
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	actor, readOnly := sharedAppMember(t, *e.Store(), org, state.OrgRoleDeveloper, []string{api.ScopeAppsRead})
	rec := e.Do(t, "PUT", "/v1/apps/team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, readOnly)
	if rec.Code != 403 {
		t.Fatalf("read key wrote env: %d %s", rec.Code, rec.Body)
	}
	if rec = e.Do(t, "GET", "/v1/apps/team-api", nil, readOnly); rec.Code != 200 {
		t.Fatalf("read key: %d %s", rec.Code, rec.Body)
	}
	if rec = e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "personal-api"}, nil); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	if rec = e.Do(t, "GET", "/v1/apps/personal-api", nil, readOnly); rec.Code != 404 {
		t.Fatalf("org key crossed to personal app: %d %s", rec.Code, rec.Body)
	}
	if err := (*e.Store()).RemoveOrgMember(t.Context(), org.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if rec = e.Do(t, "GET", "/v1/apps/team-api", nil, readOnly); rec.Code != 401 {
		t.Fatalf("removed member: %d %s", rec.Code, rec.Body)
	}
}

func TestSharedAppCreatorCannotBypassRoleOrMembership(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-creator", "Team Creator", api.PlanPro)
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	successor, _ := sharedAppMember(t, *e.Store(), org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	creatorID := (*e.Acct()).ID
	if err := (*e.Store()).TransferOrgOwnership(t.Context(), org.ID, creatorID, successor.ID); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).UpdateOrgMemberRole(t.Context(), org.ID, creatorID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, "PUT", "/v1/apps/team-api/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, nil)
	if rec.Code != 403 {
		t.Fatalf("creator role bypass: %d %s", rec.Code, rec.Body)
	}
	if err := (*e.Store()).RemoveOrgMember(t.Context(), org.ID, creatorID); err != nil {
		t.Fatal(err)
	}
	if rec = e.Do(t, "GET", "/v1/apps/team-api", nil, nil); rec.Code != 404 {
		t.Fatalf("removed creator bypass: %d %s", rec.Code, rec.Body)
	}
}

// The resumable upload routes name their app in the request body or the
// stored session, so the organization check every /v1/apps route gets never
// ran on them. A creator demoted to viewer, or removed from the organization,
// could still deploy into the shared app (and its secrets) through the CLI's
// default upload path.
func TestSharedAppUploadsApplyOrganizationAccess(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-uploads", "Team Uploads", api.PlanPro)
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	raw := apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{"index.js": []byte("console.log('ok')\n")})
	session := startSession(t, e, "team-api", int64(len(raw)))
	if rec := appendChunk(t, e, session.UploadID, 0, raw); rec.Code != 200 {
		t.Fatalf("append: %d %s", rec.Code, rec.Body)
	}
	successor, _ := sharedAppMember(t, *e.Store(), org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	creatorID := (*e.Acct()).ID
	if err := (*e.Store()).TransferOrgOwnership(t.Context(), org.ID, creatorID, successor.ID); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).UpdateOrgMemberRole(t.Context(), org.ID, creatorID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	start := apidserver.StartUploadRequest{AppSlug: "team-api", TotalSize: int64(len(raw))}
	if rec := e.Do(t, "POST", "/v1/uploads", start, nil); rec.Code != 403 {
		t.Fatalf("demoted creator opened an upload: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "POST", "/v1/uploads/"+session.UploadID+"/commit", nil, nil); rec.Code != 403 {
		t.Fatalf("demoted creator committed an upload: %d %s", rec.Code, rec.Body)
	}
	app, err := (*e.Store()).AppBySlug(t.Context(), "team-api")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).LatestDeployment(t.Context(), app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused upload created a deployment: %v", err)
	}
	if err := (*e.Store()).RemoveOrgMember(t.Context(), org.ID, creatorID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "POST", "/v1/uploads", start, nil); rec.Code != 404 {
		t.Fatalf("removed creator opened an upload: %d %s", rec.Code, rec.Body)
	}
}

func TestSharedAppCreatorKeepsLegacyDeploymentReceipt(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-receipts", "Team Receipts", api.PlanPro)
	if rec := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-api"}, nil); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	path := "/v1/apps/team-api/deployments"
	body := api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("a", 64)}
	created := e.Do(t, "POST", path, body, nil)
	if created.Code != 202 {
		t.Fatalf("create deployment: %d %s", created.Code, created.Body)
	}
	// This is the receipt identity used before organization authorization.
	if err := (*e.Store()).PutIdempotent(t.Context(), (*e.Acct()).ID, "POST "+path+"\nlegacy-retry", 202, created.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	replayed := e.Do(t, "POST", path, body, map[string]string{"Idempotency-Key": "legacy-retry"})
	if replayed.Code != 202 || replayed.Body.String() != created.Body.String() {
		t.Fatalf("legacy receipt repeated deployment: %d %s", replayed.Code, replayed.Body)
	}
}
