// Company project operations existed only as /v1/projects/{slug}/... routes,
// which resolve {slug} in the caller's own namespace: no member reached a
// project an adopted company owns, and project-limited agent keys were
// closed to every organization route (apps/projects review 2026-11-10, P1
// and P2).
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCompanyProjectOperationsReachableUnderTheOrganizationPath(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := t.Context()
	creator := *e.Acct()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "mirror-co", Name: "Mirror Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	// Billing adoption moves the company's resources to its own namespace.
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_mirror_co"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_mirror_co"); err != nil {
		t.Fatal(err)
	}
	// An adopted company stores its projects in its own namespace.
	project, err := store.CreateProject(ctx, state.Project{AccountID: org.ID, OrgID: org.ID, Slug: "shop", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billing := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	base := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug

	if rec := e.Do(t, http.MethodGet, base+"/environments/production/secrets", nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("admin secrets list under the organization path = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, base+"/environments/production/secrets", nil, viewer); rec.Code != http.StatusOK {
		t.Fatalf("viewer secrets list = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, base+"/environments/production/secrets", nil, billing); rec.Code != http.StatusForbidden {
		t.Fatalf("billing secrets list = %d %s, want 403", rec.Code, rec.Body)
	}
	// Writing a shared secret needs org.manage_project_secrets.
	if rec := e.Do(t, http.MethodPut, base+"/environments/production/secrets/TOKEN", api.SetProjectEnvironmentSharedSecretRequest{Value: "s3cret"}, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer secret write = %d %s, want 403", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodPut, base+"/environments/production/secrets/TOKEN", api.SetProjectEnvironmentSharedSecretRequest{Value: "s3cret"}, admin); rec.Code == http.StatusNotFound || rec.Code == http.StatusForbidden {
		t.Fatalf("admin secret write = %d %s, want the write attempted", rec.Code, rec.Body)
	}
	// The personal path still resolves the caller's own namespace.
	if rec := e.Do(t, http.MethodGet, "/v1/projects/"+project.Slug+"/environments/production/secrets", nil, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("personal path for a company project = %d %s, want 404", rec.Code, rec.Body)
	}
	// Explicit organization routes keep their own handlers.
	if rec := e.Do(t, http.MethodGet, base, nil, admin); rec.Code != http.StatusOK {
		t.Fatalf("organization project read = %d %s", rec.Code, rec.Body)
	}
	// Unreviewed operations stay refused under the organization path.
	if rec := e.Do(t, http.MethodGet, base+"/environments/production/gitops", nil, admin); rec.Code != http.StatusForbidden {
		t.Fatalf("unreviewed GitOps read = %d %s, want 403", rec.Code, rec.Body)
	}
	// Another company's slug never resolves this project.
	if rec := e.Do(t, http.MethodGet, "/v1/orgs/elsewhere/projects/"+project.Slug+"/environments/production/secrets", nil, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown organization = %d %s, want 404", rec.Code, rec.Body)
	}

	// A company agent key limited to the project reaches it, and only it.
	_, err = store.CreateProject(ctx, state.Project{AccountID: org.ID, OrgID: org.ID, Slug: "other", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/keys", api.CreateOrgAPIKeyRequest{
		Label: "shop-agent", Scopes: []string{api.ScopeAgent, api.ScopeAppsRead}, Projects: []string{project.Slug},
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("limited company key = %d %s", created.Code, created.Body)
	}
	var key api.APIKeyResponse
	if err := json.Unmarshal(created.Body.Bytes(), &key); err != nil || key.Plaintext == "" {
		t.Fatalf("key response: %v %s", err, created.Body)
	}
	limited := map[string]string{"Authorization": "Bearer " + key.Plaintext}
	if rec := e.Do(t, http.MethodGet, base, nil, limited); rec.Code != http.StatusOK {
		t.Fatalf("limited key, its project = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, base+"/environments/production/secrets", nil, limited); rec.Code != http.StatusOK {
		t.Fatalf("limited key, mirrored route of its project = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/projects/other", nil, limited); rec.Code != http.StatusNotFound {
		t.Fatalf("limited key, another project = %d %s, want 404", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/projects", nil, limited); rec.Code != http.StatusOK {
		t.Fatalf("limited key, organization project list = %d %s", rec.Code, rec.Body)
	}
}
