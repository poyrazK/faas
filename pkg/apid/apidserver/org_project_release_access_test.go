package apidserver_test

// adr: 20261021081828

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Atomic release operations follow the reviewed company project roles, and
// submission receipts stay bound to the project named in the URL.
func TestOrgProjectReleaseOperationsHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator, ctx := *e.Store(), *e.Acct(), t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "release-company", Name: "Release Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "shop", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "billing", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, Slug: "private"}); err != nil {
		t.Fatal(err)
	}
	_, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billing := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)

	// Receipts live in the project's storage namespace, like the submissions
	// the company deploy URLs record.
	receipts := map[string]string{}
	for i, slug := range []string{project.Slug, other.Slug} {
		attempt := strings.Repeat(string(rune('a'+i)), 32)
		receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: slug, Status: "processing"}
		if _, err := store.ReserveProjectSubmission(ctx, creator.ID, attempt, "fingerprint-"+slug, receipt); err != nil {
			t.Fatal(err)
		}
		receipts[slug] = attempt
	}

	base := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug
	readiness := api.ProjectDeploymentCheckRequest{Environment: "production", Workloads: []api.ProjectDeploymentCheckWorkload{{Workload: api.PlanWorkload{Name: "api", Class: "http"}}}}
	control := api.ProjectReleaseControlRequest{RequestID: strings.Repeat("c", 32), ExpectedGeneration: 1}
	promotionControl := map[string]any{"request_id": strings.Repeat("d", 32), "expected_generation": 1}
	for _, tc := range []struct {
		name, method, path string
		body               any
		headers            map[string]string
		want               int
	}{
		{"viewer reads readiness", "POST", base + "/deployment-readiness", readiness, viewer, 200},
		{"billing cannot read readiness", "POST", base + "/deployment-readiness", readiness, billing, 403},
		{"new company project readiness", "POST", "/v1/orgs/" + org.Slug + "/projects/new-shop/deployment-readiness", readiness, developer, 200},
		{"personal slug is not a company project", "POST", "/v1/orgs/" + org.Slug + "/projects/private/deployment-readiness", readiness, nil, 409},
		{"viewer reads company receipt", "GET", base + "/submissions/" + receipts[project.Slug], nil, viewer, 200},
		{"billing cannot read receipts", "GET", base + "/submissions/" + receipts[project.Slug], nil, billing, 403},
		{"receipt of another project", "GET", base + "/submissions/" + receipts[other.Slug], nil, developer, 404},
		{"personal URL hides company receipts", "GET", "/v1/project-submissions/" + receipts[project.Slug], nil, nil, 404},
		{"viewer cannot control a release", "POST", base + "/submissions/" + receipts[project.Slug] + "/abort", control, viewer, 403},
		{"developer controls only this project", "POST", base + "/submissions/" + receipts[other.Slug] + "/abort", control, developer, 404},
		{"personal URL cannot control company releases", "POST", "/v1/project-submissions/" + receipts[project.Slug] + "/abort", control, nil, 404},
		{"viewer cannot stage release secrets", "POST", base + "/release-secrets", map[string]any{}, viewer, 403},
		{"developer reaches release secret admission", "POST", base + "/release-secrets", map[string]any{}, developer, 503},
		{"viewer cannot safe deploy", "POST", base + "/safe-deploy", nil, viewer, 403},
		{"viewer cannot abort a promotion", "POST", base + "/environments/production/promotions/00000000-0000-4000-8000-000000000000/abort", promotionControl, viewer, 403},
		{"viewer cannot recover a promotion migration", "POST", base + "/environments/production/promotions/00000000-0000-4000-8000-000000000000/recover-migration", map[string]any{}, viewer, 403},
	} {
		rec := e.Do(t, tc.method, tc.path, tc.body, tc.headers)
		if rec.Code != tc.want {
			t.Fatalf("%s: %s %s = %d %s; want %d", tc.name, tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
		if tc.want == 200 && strings.Contains(tc.path, "/submissions/") {
			var got api.ProjectSubmissionReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ProjectSlug != project.Slug {
				t.Fatalf("%s: receipt %+v %v", tc.name, got, err)
			}
		}
	}
	// A personal project's receipts remain on the personal URL.
	attempt := strings.Repeat("e", 32)
	if _, err := store.ReserveProjectSubmission(ctx, creator.ID, attempt, "fingerprint-private", api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: "private", Status: "processing"}); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, "GET", "/v1/project-submissions/"+attempt, nil, nil); rec.Code != 200 {
		t.Fatalf("personal receipt: %d %s", rec.Code, rec.Body)
	}
}

// Company deploy URLs record durable submissions like the personal URL, and
// the company safe-deploy URL reaches release admission for the named project.
func TestOrgProjectDeploySubmissionHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	store, creator, ctx := *e.Store(), *e.Acct(), t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "receipt-company", Name: "Receipt Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_receipt_company"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_receipt_company"); err != nil {
		t.Fatal(err)
	}
	_, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	post := func(path, slug string, headers map[string]string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		source, err := mw.CreateFormFile("source", "app.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := source.Write(apidserver.ApplyProjectOneWorkloadTarGz(t)); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{"project_slug": slug, "no_triggers": "true"} {
			if err := mw.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, &body)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	attempt := strings.Repeat("f", 32)
	headers := map[string]string{"Authorization": developer["Authorization"], api.DeploymentAttemptHeader: attempt}
	if rec := post("/v1/orgs/"+org.Slug+"/projects", "receipt-shop", headers); rec.Code != 200 {
		t.Fatalf("company apply with attempt: %d %s", rec.Code, rec.Body)
	}
	project, err := store.ProjectByOrgSlug(ctx, org.ID, "receipt-shop")
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug
	rec := e.Do(t, "GET", base+"/submissions/"+attempt, nil, viewer)
	var receipt api.ProjectSubmissionReceipt
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &receipt) != nil || receipt.ProjectSlug != project.Slug || receipt.Apply.ProjectID != project.ID {
		t.Fatalf("company receipt: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, "GET", "/v1/project-submissions/"+attempt, nil, developer); rec.Code != 404 {
		t.Fatalf("personal URL exposed a company receipt: %d %s", rec.Code, rec.Body)
	}
	// Safe deploy needs an active release set; reaching that check proves the
	// company grant admitted the request for this project.
	headers[api.DeploymentAttemptHeader] = strings.Repeat("9", 32)
	rec = post(base+"/safe-deploy", project.Slug, headers)
	if rec.Code == 403 || rec.Code == 404 || !strings.Contains(rec.Body.String(), "safe deploy requires") {
		t.Fatalf("company safe deploy admission: %d %s", rec.Code, rec.Body)
	}
	headers[api.DeploymentAttemptHeader] = strings.Repeat("8", 32)
	if rec := post(base+"/safe-deploy", "another-slug", headers); rec.Code == 200 || !strings.Contains(rec.Body.String(), "must match") {
		t.Fatalf("company safe deploy accepted another project slug: %d %s", rec.Code, rec.Body)
	}
}
