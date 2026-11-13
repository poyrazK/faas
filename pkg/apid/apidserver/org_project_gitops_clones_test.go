package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// notForbidden marks a case whose role check passes; the operation itself may
// still refuse the request for its own reasons.
const notForbidden = 0

// Company projects reach GitOps, environment copies and full clones
// (ADR-20261112230000). Reads need the read role; binding or adopting a Git
// source needs the security role; approving a revision or overriding GitOps
// state in a protected environment, and copying a protected environment, need
// the approve role. A company project never binds GitLab through a member's
// own connection.
func TestOrgProjectGitOpsAndClones(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator := *e.Store(), *e.Acct()
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "gitops-company", Name: "Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "shop", ProductionBranch: "main",
		RepoFullName: "company/shop", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	_, adminHeaders := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, developerHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerHeaders := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	base := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug
	protected := true
	for _, setup := range []struct {
		body    api.CreateProjectEnvironmentRequest
		headers map[string]string
	}{
		{api.CreateProjectEnvironmentRequest{Slug: "staging"}, developerHeaders},
		{api.CreateProjectEnvironmentRequest{Slug: "secure", Protected: &protected}, adminHeaders},
	} {
		if rec := e.Do(t, "POST", base+"/environments", setup.body, setup.headers); rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", setup.body.Slug, rec.Code, rec.Body)
		}
	}
	for _, env := range []string{"staging", "secure"} {
		if _, err := store.CreateEnvironmentGitSource(ctx, creator.ID, project.ID, env, state.EnvironmentGitSourceSpec{
			Provider: "github", Repository: "company/shop", RepositoryID: 7, InstallationID: 42, Ref: "refs/heads/main",
			ManifestPath: "gregale.env.yaml", Mode: "report", ApprovalPolicy: "manual"}); err != nil {
			t.Fatalf("seed %s source: %v", env, err)
		}
	}
	revision := map[string]any{"commit_sha": strings.Repeat("a", 40), "definition_digest": strings.Repeat("b", 64), "expected_generation": 1}
	override := map[string]any{"resource": "web", "path": "env.MODE"}
	for _, tc := range []struct {
		name, method, path string
		body               any
		headers            map[string]string
		want               int
	}{
		{"viewer reads GitOps", "GET", base + "/environments/staging/gitops", nil, viewerHeaders, http.StatusOK},
		{"viewer may not bind a source", "POST", base + "/environments/staging/gitops/source", api.CreateEnvironmentGitSourceRequest{ManifestPath: "gregale.env.yaml"}, viewerHeaders, http.StatusForbidden},
		{"developer may not bind a source", "POST", base + "/environments/staging/gitops/source", api.CreateEnvironmentGitSourceRequest{ManifestPath: "gregale.env.yaml"}, developerHeaders, http.StatusForbidden},
		{"developer may not adopt", "POST", base + "/environments/staging/gitops/adopt", map[string]any{"plan_hash": strings.Repeat("c", 64)}, developerHeaders, http.StatusForbidden},
		{"admin may not bind GitLab", "POST", base + "/environments/staging/gitops/source", api.CreateEnvironmentGitSourceRequest{Provider: "gitlab", Repository: "company/shop", Ref: "refs/heads/main", ManifestPath: "gregale.env.yaml"}, adminHeaders, http.StatusConflict},
		{"developer overrides an unprotected environment", "POST", base + "/environments/staging/gitops/overrides", override, developerHeaders, notForbidden},
		{"developer may not override a protected environment", "POST", base + "/environments/secure/gitops/overrides", override, developerHeaders, http.StatusForbidden},
		{"developer may not remove a protected override", "DELETE", base + "/environments/secure/gitops/overrides", override, developerHeaders, http.StatusForbidden},
		{"admin overrides a protected environment", "POST", base + "/environments/secure/gitops/overrides", override, adminHeaders, notForbidden},
		{"viewer may not approve a revision", "POST", base + "/environments/staging/gitops/revisions/approve", revision, viewerHeaders, http.StatusForbidden},
		{"developer approves into an unprotected environment", "POST", base + "/environments/staging/gitops/revisions/approve", revision, developerHeaders, notForbidden},
		{"developer may not approve into a protected environment", "POST", base + "/environments/secure/gitops/revisions/approve", revision, developerHeaders, http.StatusForbidden},
		{"admin approves into a protected environment", "POST", base + "/environments/secure/gitops/revisions/approve", revision, adminHeaders, notForbidden},
		{"viewer may not copy an environment", "POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "copy-v", FromEnvironment: "staging"}, viewerHeaders, http.StatusForbidden},
		{"developer copies an unprotected environment", "POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "copy-d", FromEnvironment: "staging"}, developerHeaders, http.StatusCreated},
		{"developer may not copy a protected environment", "POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "copy-s", FromEnvironment: "secure"}, developerHeaders, http.StatusForbidden},
		{"admin copies a protected environment", "POST", base + "/environments", api.CreateProjectEnvironmentRequest{Slug: "copy-a", FromEnvironment: "secure"}, adminHeaders, http.StatusCreated},
		{"developer may not fully clone a protected environment", "POST", base + "/environment-clones", api.CreateProjectEnvironmentRequest{Slug: "clone-s", FromEnvironment: "secure"}, developerHeaders, http.StatusForbidden},
		{"developer fully clones an unprotected environment", "POST", base + "/environment-clones", api.CreateProjectEnvironmentRequest{Slug: "clone-d", FromEnvironment: "staging"}, developerHeaders, notForbidden},
		{"viewer may not fully clone", "POST", base + "/environment-clones", api.CreateProjectEnvironmentRequest{Slug: "clone-v", FromEnvironment: "staging"}, viewerHeaders, http.StatusForbidden},
		{"viewer reads a clone operation", "GET", base + "/environment-clones/00000000-0000-4000-8000-000000000000", nil, viewerHeaders, notForbidden},
	} {
		rec := e.Do(t, tc.method, tc.path, tc.body, tc.headers)
		if tc.want == notForbidden && rec.Code == http.StatusForbidden || tc.want != notForbidden && rec.Code != tc.want {
			t.Errorf("%s: %s %s = %d %s; want %d (0: not 403)", tc.name, tc.method, tc.path, rec.Code, rec.Body, tc.want)
		}
		if tc.name == "admin may not bind GitLab" && !strings.Contains(rec.Body.String(), api.CodeGitLabCompanyProjectUnavailable) {
			t.Errorf("GitLab refusal = %s; want %s", rec.Body, api.CodeGitLabCompanyProjectUnavailable)
		}
	}
}
