package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Company projects can branch (ADR-20261031193635, reviewed 2026-11-12). A
// developer may branch an unprotected environment and delete a branch; a
// protected parent needs the approve role, because its branch hands the
// parent's secrets to an environment a developer may deploy to. Deleting an
// environment that is not a branch still needs the delete role.
func TestOrgProjectEnvironmentBranches(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, creator := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	if err := (*srv.RuntimeConfig()).Apply(apidserver.RuntimeConfigEnvironmentBranches, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "branch-company", Name: "Company", Plan: api.PlanPro})
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
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, ProjectID: project.ID, Slug: "company-shop-web",
		WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/web.ext4", "layers/web.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	_, adminHeaders := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, developerHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerHeaders := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	base := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug
	protected := true
	for _, tc := range []struct {
		name, method, path string
		body               any
		headers            map[string]string
		want               int
	}{
		{"developer branches production", "POST", base + "/environments/production/branches", api.CreateProjectEnvironmentBranchRequest{Slug: "pr-1", TTL: "2h"}, developerHeaders, 201},
		{"viewer may not branch", "POST", base + "/environments/production/branches", api.CreateProjectEnvironmentBranchRequest{Slug: "pr-2", TTL: "2h"}, viewerHeaders, 403},
		{"viewer lists branches", "GET", base + "/environments/production/branches", nil, viewerHeaders, 200},
		{"admin protects an environment", "POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "secure", Protected: &protected}, adminHeaders, 201},
		{"developer may not branch a protected environment", "POST", base + "/environments/secure/branches", api.CreateProjectEnvironmentBranchRequest{Slug: "secure-pr", TTL: "2h"}, developerHeaders, 403},
		{"admin branches a protected environment", "POST", base + "/environments/secure/branches", api.CreateProjectEnvironmentBranchRequest{Slug: "secure-pr", TTL: "2h"}, adminHeaders, 201},
		{"developer may not delete an environment", "DELETE", base + "/environments/secure", nil, developerHeaders, 403},
		{"viewer may not delete a branch", "DELETE", base + "/environments/pr-1", nil, viewerHeaders, 403},
		{"developer deletes a branch", "DELETE", base + "/environments/pr-1", nil, developerHeaders, 204},
		{"developer deletes a protected parent's branch", "DELETE", base + "/environments/secure-pr", nil, developerHeaders, 204},
	} {
		rec := e.Do(t, tc.method, tc.path, tc.body, tc.headers)
		if rec.Code != tc.want {
			t.Fatalf("%s: %s %s = %d %s; want %d", tc.name, tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
		if tc.name == "viewer lists branches" && !strings.Contains(rec.Body.String(), `"slug":"pr-1"`) {
			t.Fatalf("branch list = %s; want pr-1", rec.Body)
		}
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, creator.ID, project.ID, "pr-1"); err == nil {
		t.Fatal("the deleted branch still exists")
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, creator.ID, project.ID, "secure"); err != nil {
		t.Fatalf("the protected environment = %v; want it kept", err)
	}
}
