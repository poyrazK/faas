// adr: 20261110200000
package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgInstallFake records which namespace each repository listing ran for.
type orgInstallFake struct {
	apidserver.StubGithubdClient
	listedFor []string
}

func (f *orgInstallFake) ListInstallableRepos(_ context.Context, accountID string, _ int64) ([]apidserver.Repo, error) {
	f.listedFor = append(f.listedFor, accountID)
	return []apidserver.Repo{{FullName: "acme/shop"}}, nil
}

// A company project links its repository through the organization's own
// installation only; a member's personal installation that sees the same
// repository is never used (apps/projects review 2026-11-10, P4).
func TestCompanyProjectLinksThroughTheOrganizationsInstallation(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	gh := &orgInstallFake{}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, gh, nil, nil, 0, "")
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_acme"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_acme"); err != nil {
		t.Fatal(err)
	}
	if org, err = store.OrgByID(ctx, org.ID); err != nil || !org.ResourceOwnershipEnabled {
		t.Fatalf("fixture organization not adopted: %+v %v", org, err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: org.ID, OrgID: org.ID, Slug: "shop", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	admin, adminAuth := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, developerAuth := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	install := func(accountID string, id int64) {
		t.Helper()
		if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: accountID, InstallationID: id, AuditGithubLogin: "octo", SealedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	do := func(method, path string, body any, auth map[string]string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		r := httptest.NewRequest(method, path, &buf)
		r.Header.Set("Content-Type", "application/json")
		for k, v := range auth {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec
	}
	repo := "acme/shop"
	link := api.UpdateProjectRequest{RepoFullName: &repo}
	path := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug

	// The admin's personal installation can see the repository, but the
	// organization has none of its own.
	install(admin.ID, 101)
	rec := do(http.MethodPatch, path, link, adminAuth)
	if problem := decodeProblem(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || problem.Code != api.CodeGitHubInstallNotFound {
		t.Fatalf("link without an organization installation = %d %s, want 404 %s", rec.Code, rec.Body, api.CodeGitHubInstallNotFound)
	}
	for _, owner := range gh.listedFor {
		if owner == admin.ID {
			t.Fatal("a member's personal installation was consulted for a company project")
		}
	}

	install(org.ID, 202)
	if rec := do(http.MethodPatch, path, link, developerAuth); rec.Code != http.StatusForbidden {
		t.Fatalf("developer link = %d %s, want 403", rec.Code, rec.Body)
	}
	if rec := do(http.MethodPatch, path, link, adminAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin link through the organization's installation = %d %s", rec.Code, rec.Body)
	}
	updated, err := store.ProjectByOrgSlug(ctx, org.ID, project.Slug)
	if err != nil || updated.RepoFullName != repo || updated.InstallID != 202 {
		t.Fatalf("project binding = %+v %v, want %s via installation 202", updated, err, repo)
	}
	if got := gh.listedFor[len(gh.listedFor)-1]; got != org.ID {
		t.Fatalf("repositories listed for %s, want the organization's namespace", got)
	}

	// Members see the organization's installations, not personal ones.
	rec = do(http.MethodGet, "/v1/orgs/"+org.Slug+"/github/installations", nil, developerAuth)
	var listed []api.GitHubInstallationSummary
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].InstallationID != 202 {
		t.Fatalf("organization installations = %d %s, want only 202", rec.Code, rec.Body)
	}
}

// The OAuth state names the organization it was sealed for, and an owner or
// admin must still hold that role when GitHub calls back.
func TestOrganizationGitHubConnectNeedsAnOwnerOrAdmin(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_acme"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_acme"); err != nil {
		t.Fatal(err)
	}
	admin, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	developer, _ := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	outsider, err := store.CreateAccount(ctx, "outsider@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, &orgInstallFake{}, nil, nil, 0, "")
	for _, tc := range []struct {
		name   string
		acct   state.Account
		orgID  string
		want   string
		status int
	}{
		{name: "personal", acct: developer, want: developer.ID},
		{name: "admin", acct: admin, orgID: org.ID, want: org.ID},
		{name: "developer", acct: developer, orgID: org.ID, status: http.StatusForbidden},
		{name: "outsider", acct: outsider, orgID: org.ID, status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := apidserver.GitHubConnectAccountAtCallback(srv, ctx, tc.acct, tc.orgID)
			if tc.status != 0 {
				if problem == nil || problem.Status != tc.status {
					t.Fatalf("got %q %+v, want status %d", got, problem, tc.status)
				}
				return
			}
			if problem != nil || got != tc.want {
				t.Fatalf("got %q %+v, want %q", got, problem, tc.want)
			}
		})
	}

	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	token, err := apidserver.SealOAuthCodeStateForOrg(mgr, admin.ID, org.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if orgID, ok := apidserver.OpenOAuthCodeState(mgr, token, admin.ID); !ok || orgID != org.ID {
		t.Fatalf("open = %q %v, want the sealed organization", orgID, ok)
	}
	if _, ok := apidserver.OpenOAuthCodeState(mgr, token, developer.ID); ok {
		t.Fatal("a state sealed for one member opened for another")
	}
}
