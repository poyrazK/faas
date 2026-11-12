// adr: 20261104085712
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An organization's agent key can be limited to the organization's projects:
// minting resolves company project slugs, and the key then reaches its
// project's organization routes and apps, lists only that project, and sees
// the company's other projects and apps as missing.
func TestHandlers_OrgAgentKeyLimitedToACompanyProject(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator := *e.Store(), *e.Acct()
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "limit-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ project, app string }{{"shop", "limit-shop-api"}, {"blog", "limit-blog-api"}} {
		project, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: p.project, ProductionBranch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, ProjectID: project.ID, Slug: p.app, WorkloadName: "api"}); err != nil {
			t.Fatal(err)
		}
	}
	// Another company's project with the same slug is not the one limited.
	other, err := store.CreateOrg(ctx, state.Org{Slug: "limit-other", Name: "Other", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.Project{AccountID: other.ID, OrgID: other.ID, Slug: "shop"}); err != nil {
		t.Fatal(err)
	}
	_, admin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)

	keys := "/v1/orgs/" + org.Slug + "/keys"
	if rec := e.Do(t, http.MethodPost, keys, api.CreateOrgAPIKeyRequest{Label: "missing", Scopes: []string{api.ScopeAgent}, Projects: []string{"no-such-project"}}, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("a missing project = %d %s, want 404", rec.Code, rec.Body)
	}
	rec := e.Do(t, http.MethodPost, keys, api.CreateOrgAPIKeyRequest{Label: "shop-agent", Scopes: []string{api.ScopeAgent}, Projects: []string{"shop"}}, admin)
	var created api.APIKeyResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || !slices.Equal(created.Projects, []string{"shop"}) || created.Plaintext == "" {
		t.Fatalf("limited organization agent key = %d %s", rec.Code, rec.Body)
	}
	agent := map[string]string{"Authorization": "Bearer " + created.Plaintext}

	var projects []api.ProjectSummaryResponse
	if rec := e.Do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/projects", nil, agent); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &projects) != nil || len(projects) != 1 || projects[0].Slug != "shop" {
		t.Fatalf("GET organization projects = %d %s; want only shop", rec.Code, rec.Body)
	}
	base := "/v1/orgs/" + org.Slug
	for path, want := range map[string]int{
		base + "/projects/shop":                      http.StatusOK,
		base + "/projects/shop/environments":         http.StatusOK,
		base + "/projects/blog":                      http.StatusNotFound,
		base + "/projects/blog/environments":         http.StatusNotFound,
		base + "/apps/limit-blog-api/queue-bindings": http.StatusNotFound,
		"/v1/orgs/" + other.Slug + "/projects/shop":  http.StatusNotFound,
		"/v1/apps/limit-shop-api":                    http.StatusOK,
		"/v1/apps/limit-blog-api":                    http.StatusNotFound,
	} {
		if rec := e.Do(t, http.MethodGet, path, nil, agent); rec.Code != want {
			t.Errorf("GET %s = %d %s, want %d", path, rec.Code, rec.Body, want)
		}
	}
	// The project's own app passes the limit; the handler decides the rest.
	if rec := e.Do(t, http.MethodGet, base+"/apps/limit-shop-api/queue-bindings", nil, agent); rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
		t.Errorf("the limited project's app route = %d %s; want it past the project limit", rec.Code, rec.Body)
	}
}
