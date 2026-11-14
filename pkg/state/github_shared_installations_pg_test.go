package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgSharedGitHubInstallationHealthAndRevocation(t *testing.T) {
	store, ctx := pgStore(t)
	var accounts []state.Account
	for _, email := range []string{"first-shared@example.com", "second-shared@example.com"} {
		account, err := store.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account)
		if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: account.ID, InstallationID: 4242, AuditGithubLogin: "octocat", SealedToken: []byte(email), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shared", InstallID: 4242, RepoFullName: "octocat/shared", ProductionBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := store.GitHubProjectBindingsForInstallation(ctx, 4242)
	if err != nil || len(targets) != 2 {
		t.Fatalf("project targets = %+v %v", targets, err)
	}
	if err := store.RecordGitHubInstallationSync(ctx, 4242, time.Now(), "", 3, 0); err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		install, err := store.GitHubInstallForAccountInstallation(ctx, account.ID, 4242)
		if err != nil || install.LastReconciledAt == nil || install.LastReconcileRepositoryCount != 3 {
			t.Fatalf("health for %s: %+v %v", account.ID, install, err)
		}
	}
	if err := store.RevokeGitHubInstallation(ctx, 4242); err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if _, err := store.GitHubInstallForAccountInstallation(ctx, account.ID, 4242); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("revoked credentials retained: %v", err)
		}
		projects, err := store.ListProjectsForAccount(ctx, account.ID)
		if err != nil || len(projects) != 1 || projects[0].InstallID != 0 {
			t.Fatalf("revoked project retained access: %+v %v", projects, err)
		}
	}
}

func TestPgAccountDisconnectPreservesOtherConnectionAndLogin(t *testing.T) {
	store, ctx := pgStore(t)
	var accounts []state.Account
	for _, name := range []string{"first", "second"} {
		account, err := store.CreateAccount(ctx, "detach-"+name+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account)
		if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: account.ID, InstallationID: 42, AuditGithubLogin: "octocat", SealedToken: []byte(name), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "detach", InstallID: 42, RepoFullName: "octo/shared", ProductionBranch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "detach-" + name, RAMMB: 128, ProjectID: project.ID, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AccountID: account.ID, AppID: app.ID, BindingID: "bind-" + app.ID, InstallID: 42, RepoFullName: "octo/shared", ProductionBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	first := accounts[0]
	second := accounts[1]
	if err := store.UpsertOAuthLink(ctx, first.ID, "github", "shared-login", "first@example.test", true); err != nil {
		t.Fatal(err)
	}
	if err := store.DisconnectGitHubAccountInstallation(ctx, first.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.DisconnectGitHubAccountInstallation(ctx, first.ID, 42); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GitHubInstallForAccountInstallation(ctx, first.ID, 42); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("credential survives: %v", err)
	}
	link, err := store.OAuthLinkByProviderSubject(ctx, "github", "shared-login")
	if err != nil || link.AccountID != first.ID {
		t.Fatalf("login changed after disconnect: %+v %v", link, err)
	}
	if err := store.UpsertOAuthLink(ctx, second.ID, "github", "shared-login", "second@example.test", true); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("login exclusivity lost: %v", err)
	}
	other, err := store.GitHubInstallForAccountInstallation(ctx, second.ID, 42)
	if err != nil || string(other.SealedToken) != "second" {
		t.Fatalf("other credential changed: %+v %v", other, err)
	}
	for i, account := range accounts {
		bindings, err := store.ListGithubInstallBindingsForAccount(ctx, account.ID)
		if err != nil || len(bindings) != i {
			t.Fatalf("wrong binding scope %d: %v %v", i, bindings, err)
		}
		projects, err := store.ListProjectsForAccount(ctx, account.ID)
		if err != nil || len(projects) != 1 || (projects[0].InstallID == 42) != (i == 1) {
			t.Fatalf("wrong project scope: %v %v", projects, err)
		}
	}
	if err := store.RefreshGitHubConnectionToken(ctx, []byte("first"), state.GitHubInstall{AccountID: first.ID, InstallationID: 42, SealedToken: []byte("late"), TokenExpiresAt: time.Now().Add(time.Hour)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late refresh resurrects disconnect: %v", err)
	}
}
