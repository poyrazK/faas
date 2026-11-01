// adr: 20261101160300
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The production trigger of an app bound without a project lives on its
// binding: every binding read returns it, a re-bind keeps it and an unbind
// clears it.
func TestPgStoreGitHubAppProductionTrigger(t *testing.T) {
	store, ctx := pgStore(t)
	acct, err := store.CreateAccount(ctx, "github-trigger-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "trigger-pg", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	installation := time.Now().UnixNano()%1_000_000_000 + 1
	bind := func() {
		t.Helper()
		if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: app.ID, AccountID: acct.ID, BindingID: "bind-" + app.ID,
			InstallID: installation, RepoFullName: "acme/api", ProductionBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetGitHubAppProductionTrigger(ctx, app.ID, acct.ID, state.ProductionTriggerActions); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unbound app: err = %v, want ErrNotFound", err)
	}
	bind()
	if err := store.SetGitHubAppProductionTrigger(ctx, app.ID, acct.ID, "bogus"); err == nil {
		t.Fatal("an invalid trigger was stored")
	}
	if err := store.SetGitHubAppProductionTrigger(ctx, app.ID, acct.ID, state.ProductionTriggerActions); err != nil {
		t.Fatal(err)
	}
	bind() // a re-bind keeps the trigger
	byApp, err := store.GetGithubInstallBindingForApp(ctx, app.ID, acct.ID)
	if err != nil || byApp.ProductionTrigger != state.ProductionTriggerActions {
		t.Fatalf("binding for app = %+v, %v", byApp, err)
	}
	byRepo, err := store.GithubInstallBindingForRepoBranchInstallation(ctx, "acme/api", "main", installation)
	if err != nil || byRepo.ProductionTrigger != state.ProductionTriggerActions {
		t.Fatalf("binding for repository = %+v, %v", byRepo, err)
	}
	all, err := store.ListGithubInstallBindingsForAccount(ctx, acct.ID)
	if err != nil || all[app.ID].ProductionTrigger != state.ProductionTriggerActions {
		t.Fatalf("account bindings = %+v, %v", all, err)
	}
	if err := store.DeleteGithubInstallBinding(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	bind()
	if after, err := store.GetGithubInstallBindingForApp(ctx, app.ID, acct.ID); err != nil || after.ProductionTrigger != "" {
		t.Fatalf("binding after unbind and bind = %+v, %v; want the webhook", after, err)
	}
}

// A renamed organization moves its installation's bindings and projects; a
// renamed repository moves its project too.
func TestPgStoreRenameGitHubAccountAndRepository(t *testing.T) {
	store, ctx := pgStore(t)
	acct, err := store.CreateAccount(ctx, "github-rename-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	installation := time.Now().UnixNano()%1_000_000_000 + 1
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rename-pg", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: app.ID, AccountID: acct.ID, BindingID: "bind-" + app.ID,
		InstallID: installation, RepoFullName: "Acme/api", ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rename-pg", InstallID: installation,
		RepoFullName: "acme/web", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "rename-pg-other", InstallID: installation,
		RepoFullName: "acmesoft/web", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RenameGitHubAccount(ctx, installation, "acme", "acme-inc"); err != nil {
		t.Fatal(err)
	}
	if binding, err := store.GetGithubInstallBindingForApp(ctx, app.ID, acct.ID); err != nil || binding.RepoFullName != "acme-inc/api" {
		t.Fatalf("binding = %+v, %v; want acme-inc/api", binding, err)
	}
	if got, err := store.ProjectByID(ctx, project.ID); err != nil || got.RepoFullName != "acme-inc/web" {
		t.Fatalf("project = %+v, %v; want acme-inc/web", got, err)
	}
	if got, err := store.ProjectByID(ctx, other.ID); err != nil || got.RepoFullName != "acmesoft/web" {
		t.Fatalf("another owner's project = %+v, %v; want it unchanged", got, err)
	}

	if err := store.RenameGitHubRepository(ctx, installation, "acme-inc/web", "acme-inc/site"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ProjectByRepo(ctx, acct.ID, installation, "acme-inc/site"); err != nil || got.ID != project.ID {
		t.Fatalf("project by the new name = %+v, %v", got, err)
	}
}
