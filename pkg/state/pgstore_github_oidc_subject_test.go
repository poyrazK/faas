//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_AccountByOIDCSubject_GitHubImmutableBindingIDs(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "github-immutable")
	const installationID = int64(424242)
	if err := s.UpsertGitHubInstall(ctx, state.GitHubInstall{
		AccountID: accountID, InstallationID: installationID,
		SealedToken: []byte("test-sealed-token"), TokenExpiresAt: time.Now().Add(time.Hour),
		AuditGithubLogin: "octocat",
	}); err != nil {
		t.Fatalf("UpsertGitHubInstall: %v", err)
	}
	if err := s.UpsertGithubInstallBinding(ctx, state.GitHubBinding{
		AppID: appID, AccountID: accountID, BindingID: "bind-github-immutable",
		InstallID: installationID, RepoFullName: "octocat/hello",
		OwnerID: 123456, RepoID: 789012, ProductionBranch: "main",
	}); err != nil {
		t.Fatalf("UpsertGithubInstallBinding: %v", err)
	}

	const issuer = "https://token.actions.githubusercontent.com"
	for _, subject := range []string{
		"repo:octocat/hello:environment:production",
		"repo:octocat@123456/hello@789012:environment:production",
	} {
		got, err := s.AccountByOIDCSubject(ctx, issuer, subject)
		if err != nil {
			t.Fatalf("AccountByOIDCSubject(%q): %v", subject, err)
		}
		if got.ID != accountID {
			t.Fatalf("AccountByOIDCSubject(%q) = %q, want %q", subject, got.ID, accountID)
		}
	}
	for _, subject := range []string{
		"repo:octocat@654321/hello@789012:environment:production",
		"repo:octocat@123456/hello@987654:environment:production",
	} {
		if _, err := s.AccountByOIDCSubject(ctx, issuer, subject); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("AccountByOIDCSubject(%q) error = %v, want ErrNotFound", subject, err)
		}
	}
}
