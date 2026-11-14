package conformance

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testGitHubDeploymentConnections(t *testing.T, fx *Fixture) {
	other, err := fx.Store.CreateAccount(fx.Ctx, "github-install-other@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	inst := state.GitHubInstall{
		AccountID: fx.Account.ID, InstallationID: 4242, AuditGithubLogin: "octocat",
		DefaultBranch: "main", SealedToken: []byte("first-sealed-token"), TokenExpiresAt: time.Now().Add(time.Hour),
	}
	if err := fx.Store.UpsertGitHubInstall(fx.Ctx, inst); err != nil {
		t.Fatal(err)
	}
	inst.SealedToken = []byte("refreshed-sealed-token")
	if err := fx.Store.UpsertGitHubInstall(fx.Ctx, inst); err != nil {
		t.Fatalf("same-account reconnect: %v", err)
	}
	claim := inst
	claim.AccountID, claim.SealedToken = other.ID, []byte("other-sealed-token")
	if err := fx.Store.UpsertGitHubInstall(fx.Ctx, claim); err != nil {
		t.Fatalf("independent deployment connection: %v", err)
	}
	got, err := fx.Store.GitHubInstallForAccountInstallation(fx.Ctx, fx.Account.ID, inst.InstallationID)
	if err != nil || got.AccountID != inst.AccountID || !bytes.Equal(got.SealedToken, inst.SealedToken) {
		t.Fatalf("original credentials changed after another account connected: %v", err)
	}
	second, err := fx.Store.GitHubInstallForAccountInstallation(fx.Ctx, other.ID, inst.InstallationID)
	if err != nil || !bytes.Equal(second.SealedToken, claim.SealedToken) {
		t.Fatalf("second credentials: %v", err)
	}
	if _, err := fx.Store.GitHubInstallForAccountInstallation(fx.Ctx, fx.Account.ID, 999999); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unconnected installation accessible: %v", err)
	}
	// Deployment authorization must not create or move a login identity.
	if err := fx.Store.UpsertOAuthLink(fx.Ctx, fx.Account.ID, "github", "12345", "first@example.com", true); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpsertOAuthLink(fx.Ctx, other.ID, "github", "12345", "second@example.com", true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("GitHub login must remain exclusive: %v", err)
	}
	link, err := fx.Store.OAuthLinkByProviderSubject(fx.Ctx, "github", "12345")
	if err != nil || link.AccountID != fx.Account.ID || link.Email != "first@example.com" {
		t.Fatalf("login identity moved: %+v %v", link, err)
	}
	// Both accounts can import the same repository, with isolated project rows.
	for _, accountID := range []string{fx.Account.ID, other.ID} {
		project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: accountID, Slug: "shared-repo", InstallID: inst.InstallationID, RepoFullName: "octocat/shared", ProductionBranch: "main"})
		if err != nil {
			t.Fatalf("import repository for %s: %v", accountID, err)
		}
		found, err := fx.Store.ProjectByRepo(fx.Ctx, accountID, inst.InstallationID, "octocat/shared")
		if err != nil || found.ID != project.ID || found.AccountID != accountID {
			t.Fatalf("project ownership: %+v %v", found, err)
		}
	}
	if _, err := fx.Store.ProjectByRepo(fx.Ctx, "", inst.InstallationID, "octocat/shared"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ambiguous unscoped project: %v", err)
	}

	// Simultaneous authorizations persist both account connections.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, accountID := range []string{fx.Account.ID, other.ID} {
		concurrent := inst
		concurrent.AccountID, concurrent.InstallationID = accountID, 4243
		go func() {
			<-start
			results <- fx.Store.UpsertGitHubInstall(fx.Ctx, concurrent)
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent connection: %v", err)
		}
	}
	for _, accountID := range []string{fx.Account.ID, other.ID} {
		got, err := fx.Store.GitHubInstallForAccountInstallation(fx.Ctx, accountID, 4243)
		if err != nil || got.AccountID != accountID {
			t.Fatalf("concurrent authorization lost: %v", err)
		}
	}
}
