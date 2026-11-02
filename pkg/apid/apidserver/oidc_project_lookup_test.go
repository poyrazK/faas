// adr: 20261101160300
package apidserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/oidcrepo"
	"github.com/onebox-faas/faas/pkg/oidc"
	"github.com/onebox-faas/faas/pkg/state"
)

// installationRepos answers githubd's repository listing for one
// installation.
type installationRepos map[int64][]oidcrepo.Repo

func (r installationRepos) ListInstallableRepos(_ context.Context, _ string, installationID int64) ([]oidcrepo.Repo, error) {
	return r[installationID], nil
}

// A GitHub Actions workflow of a repository connected only through a project
// signs in as the project's account, and must present the repository ID
// githubd reports for it (ADR-20261101160300, amendment 1).
func TestOIDCAccountLookupConfirmsProjectRepository(t *testing.T) {
	ctx := context.Background()
	const issuer = "https://token.actions.githubusercontent.com"
	const subject = "repo:acme/shop:ref:refs/heads/main"
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "shop-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 77, AuditGithubLogin: "acme",
		SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(ctx, state.Project{
		AccountID: acct.ID, Slug: "shop", RepoFullName: "acme/shop", InstallID: 77,
		ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention,
	}); err != nil {
		t.Fatal(err)
	}

	lookup := apidserver.OIDCAccountLookup(store, installationRepos{77: {{ID: 4242, FullName: "Acme/Shop"}}})
	got, err := lookup.AccountByOIDCSubject(ctx, issuer, subject)
	if err != nil || got.ID != acct.ID {
		t.Fatalf("AccountByOIDCSubject = %q, %v; want the project's account", got.ID, err)
	}
	ids, ok := lookup.(oidc.RepositoryIDLookup)
	if !ok {
		t.Fatal("lookup does not resolve repository IDs")
	}
	if owner, repo, err := ids.GitHubRepositoryIDsForOIDCSubject(ctx, acct.ID, issuer, subject); err != nil || owner != 0 || repo != 4242 {
		t.Fatalf("repository IDs = %d, %d, %v; want the confirmed repository 4242", owner, repo, err)
	}

	// The installation no longer sees the repository: the name proves
	// nothing.
	gone := apidserver.OIDCAccountLookup(store, installationRepos{77: {{ID: 9, FullName: "acme/web"}}})
	if got, err := gone.AccountByOIDCSubject(ctx, issuer, subject); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AccountByOIDCSubject without the repository = %q, %v; want ErrNotFound", got.ID, err)
	}
	// Without githubd, a project binding confirms nothing.
	if got, err := apidserver.OIDCAccountLookup(store, nil).AccountByOIDCSubject(ctx, issuer, subject); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AccountByOIDCSubject without githubd = %q, %v; want ErrNotFound", got.ID, err)
	}
}
