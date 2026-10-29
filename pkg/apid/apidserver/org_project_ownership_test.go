package apidserver_test

// adr: 202610171905

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgProjectOwnershipHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	orgProjectHTTP(t, *e.Store(), *e.Acct(), e.Do)
}

func orgProjectHTTP(t *testing.T, store state.Store, creator state.Account, do func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder) {
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "project-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_http_project_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_http_project_company"); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "shop", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, ProjectID: project.ID, Slug: "company-shop-api", WorkloadName: "api"})
	if err != nil {
		t.Fatal(err)
	}
	admin, adminHeaders := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	developer, developerHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerHeaders := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billingHeaders := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	base := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug
	personal, err := store.CreateProject(ctx, state.Project{AccountID: developer.ID, Slug: "private-project"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(t, "GET", "/v1/projects/"+personal.Slug, nil, developerHeaders); rec.Code != 404 {
		t.Fatalf("organization key accessed a personal project: %d %s", rec.Code, rec.Body)
	}
	foreign, err := store.CreateOrg(ctx, state.Org{Slug: "foreign-project-company", Name: "Foreign", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, foreign.ID, developer.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.Project{AccountID: foreign.ID, OrgID: foreign.ID, Slug: project.Slug}); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, "GET", "/v1/orgs/"+foreign.Slug+"/projects/"+project.Slug, nil, developerHeaders); rec.Code != 404 {
		t.Fatalf("organization key crossed companies: %d %s", rec.Code, rec.Body)
	}
	protected := true
	for _, tc := range []struct {
		method, path string
		body         any
		headers      map[string]string
		want         int
	}{
		{"GET", base, nil, developerHeaders, 200},
		{"GET", base, nil, viewerHeaders, 200},
		{"GET", base, nil, billingHeaders, 403},
		{"GET", "/v1/orgs/" + org.Slug + "/projects", nil, viewerHeaders, 200},
		{"POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "staging"}, developerHeaders, 201},
		{"POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "forbidden"}, viewerHeaders, 403},
		{"POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "refused-clone", FromEnvironment: "production"}, developerHeaders, 409},
		{"POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "refused-full", FromEnvironment: "production", Full: true}, developerHeaders, 409},
		{"POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "refused-protection", Protected: &protected}, developerHeaders, 403},
		{"PATCH", base + "/environments/production", map[string]bool{"protected": false}, developerHeaders, 403},
		{"PATCH", base, map[string]string{"production_branch": "release"}, developerHeaders, 403},
		{"POST", base + "/environments/production/approvals", map[string]string{"promotion_token": "untrusted"}, developerHeaders, 403},
		{"POST", base + "/environments/production/promote", map[string]string{}, viewerHeaders, 403},
		{"DELETE", base, nil, developerHeaders, 403},
	} {
		rec := do(t, tc.method, tc.path, tc.body, tc.headers)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: %d %s; want %d", tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
	}
	// A company project's promotion could be started and rolled back, but
	// aborting or recovering it, recovering its migration and previewing it
	// with release secrets were unreviewed routes that answered 403 to every
	// member. Developers reach the handlers (which refuse these bodies on
	// their own terms); viewers still may not.
	for _, path := range []string{
		base + "/environments/production/promotions/missing/abort",
		base + "/environments/production/promotions/missing/recover-migration",
		base + "/environments/production/promotion-preview",
	} {
		if rec := do(t, "POST", path, map[string]string{}, developerHeaders); rec.Code == 403 || strings.Contains(rec.Body.String(), "no such API route") {
			t.Fatalf("developer POST %s: %d %s; want the handler's answer", path, rec.Code, rec.Body)
		}
		if rec := do(t, "POST", path, map[string]string{}, viewerHeaders); rec.Code != 403 {
			t.Fatalf("viewer POST %s: %d %s; want 403", path, rec.Code, rec.Body)
		}
	}
	// Same key with a different actor must execute a distinct configuration write.
	for i, headers := range []map[string]string{developerHeaders, adminHeaders} {
		headers["Idempotency-Key"] = "same-project-configuration-key"
		rec := do(t, "PUT", base+"/environments/staging/config", api.UpdateProjectEnvironmentConfigRequest{Values: json.RawMessage(`{"VALUE":"` + []string{"developer", "admin"}[i] + `"}`)}, headers)
		if rec.Code != 200 {
			t.Fatalf("configuration: %d %s", rec.Code, rec.Body)
		}
	}
	config, err := store.ProjectEnvironmentConfigLatest(ctx, creator.ID, project.ID, "staging")
	if err != nil || config.Version != 2 || !strings.Contains(string(config.Values), "admin") {
		t.Fatalf("another actor replayed a receipt: %+v %v", config, err)
	}
	// Remove the original company owner before exercising release operations.
	if err := store.TransferOrgOwnership(ctx, org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, "GET", "/v1/projects/"+project.Slug, nil, nil); rec.Code != 404 {
		t.Fatalf("legacy creator access survived offboarding: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, "GET", "/v1/projects", nil, nil); rec.Code != 200 || strings.Contains(rec.Body.String(), project.ID) {
		t.Fatalf("legacy inventory leaked company project: %d %s", rec.Code, rec.Body)
	}
	if err := store.MarkAccountDeletionPending(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, "GET", base, nil, developerHeaders); rec.Code != 200 {
		t.Fatalf("creator deletion broke project: %d %s", rec.Code, rec.Body)
	}
	image := do(t, "POST", "/v1/apps/"+app.Slug+"/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("c", 64)}, developerHeaders)
	if image.Code != 202 {
		t.Fatalf("teammate deployment after creator erasure: %d %s", image.Code, image.Body)
	}
	// Real authenticated app deployment records the teammate, then promotion and
	// rollback continue to use the creator's stable resource namespace.
	deployments := map[string]state.Deployment{}
	for _, scope := range []string{"staging", "production"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat(map[string]string{"staging": "a", "production": "b"}[scope], 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/rootfs/"+scope, "project/"+scope, 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		deployments[scope] = dep
	}
	rec := do(t, "GET", base+"/environments/production/promotion-preview?from=staging", nil, developerHeaders)
	if rec.Code != 200 {
		t.Fatalf("promotion preview: %d %s", rec.Code, rec.Body)
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	rec = do(t, "POST", base+"/environments/production/approvals", api.CreateProjectEnvironmentApprovalRequest{PromotionToken: preview.PromotionToken}, adminHeaders)
	if rec.Code != 201 {
		t.Fatalf("admin approval: %d %s", rec.Code, rec.Body)
	}
	var approval api.ProjectEnvironmentApprovalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &approval); err != nil {
		t.Fatal(err)
	}
	developerHeaders["Idempotency-Key"] = "project-promotion"
	rec = do(t, "POST", base+"/environments/production/promote", api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approval.ApprovalToken}, developerHeaders)
	if rec.Code != 200 {
		t.Fatalf("teammate promotion after erasure: %d %s", rec.Code, rec.Body)
	}
	var promoted api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &promoted); err != nil {
		t.Fatal(err)
	}
	live, err := store.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil || live.ID == deployments["production"].ID || live.DeployedByUserID != developer.ID {
		t.Fatalf("promotion actor/artifact: %+v %v", live, err)
	}
	developerHeaders["Idempotency-Key"] = "project-rollback"
	rec = do(t, "POST", base+"/environments/production/promotions/"+promoted.PromotionID+"/rollback", nil, developerHeaders)
	if rec.Code != 200 {
		t.Fatalf("teammate rollback after erasure: %d %s", rec.Code, rec.Body)
	}
	live, err = store.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil || live.ID != deployments["production"].ID {
		t.Fatalf("rollback did not restore original deployment: %+v %v", live, err)
	}
	// A response cached before demotion cannot bypass the new role.
	if err := store.UpdateOrgMemberRole(ctx, org.ID, developer.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	rec = do(t, "POST", base+"/environments/production/promotions/"+promoted.PromotionID+"/rollback", nil, developerHeaders)
	if rec.Code != 403 {
		t.Fatalf("demoted actor replayed a write: %d %s", rec.Code, rec.Body)
	}
}

func TestOrgProjectSourceDeploymentHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	orgProjectSourceHTTP(t, *e.Store(), *e.Acct(), *e.H())
}

func orgProjectSourceHTTP(t *testing.T, store state.Store, creator state.Account, handler http.Handler) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "source-project-company", Name: "Source Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(t.Context(), org.ID, "cus_source_project_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), org.ID, "si_source_project_company"); err != nil {
		t.Fatal(err)
	}
	member, headers := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	post := func(path, slug string, manifest ...string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		source, err := mw.CreateFormFile("source", "myapp.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		archive := apidserver.ApplyProjectOneWorkloadTarGz(t)
		if len(manifest) > 0 {
			archive = orgProjectArchive(t, manifest[0])
		}
		if _, err := source.Write(archive); err != nil {
			t.Fatal(err)
		}
		if err := mw.WriteField("project_slug", slug); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, &body)
		req.Header.Set("Authorization", headers["Authorization"])
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	base := "/v1/orgs/" + org.Slug + "/projects"
	// A new organization uses its own storage namespace from the first upload.
	rec := post(base, "new-company-project")
	if rec.Code != 200 {
		t.Fatalf("organization project source apply: %d %s", rec.Code, rec.Body)
	}
	blocked := post(base, "personal-data-attempt", "databases:\n  - database: personal-database\n")
	if blocked.Code != 503 {
		t.Fatalf("unavailable database binding: %d %s", blocked.Code, blocked.Body)
	}
	if _, err := store.ProjectByOrgSlug(t.Context(), org.ID, "personal-data-attempt"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused binding partially created a project: %v", err)
	}
	async := post(base, "unsupported-async", "async_routes: []\n")
	if async.Code != 409 {
		t.Fatalf("unreviewed async manifest accepted: %d %s", async.Code, async.Body)
	}
	project, err := store.ProjectByOrgSlug(t.Context(), org.ID, "new-company-project")
	if err != nil || project.AccountID != org.ID {
		t.Fatalf("new project borrowed a human namespace: %+v %v", project, err)
	}
	apps, err := store.AppsForProject(t.Context(), project.AccountID, project.ID)
	if err != nil || len(apps) != 1 || apps[0].OrgID != org.ID {
		t.Fatalf("workloads lost company ownership: %+v %v", apps, err)
	}
	deps, err := store.ListDeploymentsForApp(t.Context(), apps[0].ID, 10, 0)
	if err != nil || len(deps) != 1 || deps[0].DeployedByUserID != member.ID {
		t.Fatalf("source deployment actor: %+v %v; apply=%s", deps, err, rec.Body)
	}
	// A legacy namespace remains usable through the project URL after erasure.
	legacy, err := store.CreateProject(t.Context(), state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "legacy-company-project", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(t.Context(), creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(t.Context(), creator.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := store.ProjectByOrgSlug(t.Context(), org.ID, legacy.Slug)
	if err != nil || retained.ID != legacy.ID || retained.OrgID != org.ID {
		t.Fatalf("empty company project lost after creator erasure: %+v %v; before=%+v", retained, err, legacy)
	}
	if _, err := state.ProjectResourceAccount(t.Context(), store, retained); err != nil {
		t.Fatalf("empty company project policy after erasure: %v", err)
	}
	rec = post(base+"/"+legacy.Slug+"/deploy", legacy.Slug)
	if rec.Code != 200 {
		t.Fatalf("legacy project source apply after erasure: %d %s", rec.Code, rec.Body)
	}
	apps, err = store.AppsForProject(t.Context(), legacy.AccountID, legacy.ID)
	if err != nil || len(apps) != 1 || apps[0].OrgID != org.ID || apps[0].AccountID != creator.ID {
		t.Fatalf("legacy workload namespace changed: %+v %v; apply=%s", apps, err, rec.Body)
	}
	rate, err := store.(state.AccountDeployRateStore).ReadAccountDeployRate(t.Context(), org.ID, api.PlanPro.DeploysPerHour(), time.Now().UTC())
	if err != nil || rate.Used != 2 {
		t.Fatalf("organization source deployments did not share admission: %+v %v", rate, err)
	}

}

func orgProjectArchive(t *testing.T, manifest string) []byte {
	t.Helper()
	var data bytes.Buffer
	gzipWriter := gzip.NewWriter(&data)
	archive := tar.NewWriter(gzipWriter)
	for _, entry := range []struct{ name, body string }{{"myapp/Dockerfile", "FROM alpine\n"}, {"myapp/index.js", "exports.handler = () => 1;\n"}, {"gregale.yaml", manifest}} {
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
