package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// prEnvironmentFixture is a project bound to a repository through its web
// app, with a worker service, and PR #7's environment previewing both.
type prEnvironmentFixture struct {
	acct                       state.Account
	project                    state.Project
	web                        state.App
	webPreview, workerPreview  state.App
	installation               int64
	repo                       string
	otherRepoPreview, otherWeb state.App
}

func seedPREnvironment(t *testing.T, store *state.PgStore) prEnvironmentFixture {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	f := prEnvironmentFixture{installation: time.Now().UnixNano()%1_000_000_000 + 1, repo: "acme/shop-" + suffix}
	var err error
	if f.acct, err = store.CreateAccount(ctx, "pr-env-lifecycle-"+suffix+"@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.acct.ID, Slug: "shop-" + suffix,
		InstallID: f.installation, RepoFullName: f.repo, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	app := func(slug, projectID, workload string) state.App {
		t.Helper()
		created, err := store.CreateApp(ctx, state.App{AccountID: f.acct.ID, ProjectID: projectID, Slug: slug, WorkloadName: workload,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	bind := func(production state.App, repo string) {
		t.Helper()
		if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: production.ID, AccountID: f.acct.ID,
			BindingID: "bind-" + production.ID, InstallID: f.installation, RepoFullName: repo, ProductionBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	expires := time.Now().Add(time.Hour)
	preview := func(parent state.App) state.App {
		t.Helper()
		created, err := store.CreateApp(ctx, state.App{AccountID: f.acct.ID, Slug: "pr-7-" + parent.Slug,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: parent.Slug,
			PreviewPrNumber: 7, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	f.web = app("web-"+suffix, f.project.ID, "web")
	worker := app("worker-"+suffix, f.project.ID, "worker") // a project service: no binding of its own
	bind(f.web, f.repo)
	f.webPreview, f.workerPreview = preview(f.web), preview(worker)
	if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: f.installation, RepoFullName: f.repo, PRNumber: 7,
		CommitSHA: strings.Repeat("a", 40), RootAppID: f.webPreview.ID,
		MemberAppIDs: []string{f.webPreview.ID, f.workerPreview.ID}}); err != nil {
		t.Fatal(err)
	}
	// Another repository of the same installation keeps its previews when
	// only the first repository is removed.
	f.otherWeb = app("blog-"+suffix, "", "")
	bind(f.otherWeb, "acme/blog-"+suffix)
	f.otherRepoPreview = preview(f.otherWeb)
	return f
}

type previewStateWant struct {
	app  state.App
	want string
}

func assertPreviewStates(t *testing.T, store *state.PgStore, wants ...previewStateWant) {
	t.Helper()
	for _, w := range wants {
		got, err := store.AppByID(context.Background(), w.app.ID)
		if err != nil || got.PreviewPrState != w.want {
			t.Fatalf("%s = (%q, %v), want %q", w.app.Slug, got.PreviewPrState, err, w.want)
		}
	}
}

// Uninstalling the GitHub App retired only the bound app's own preview: a PR
// environment's other services kept serving PR code to the end of their lease
// (ADR-20261102100000). Its project stayed on the gone installation, so
// reconnecting the app did not bring the project back.
func TestPgStoreRevokeGitHubInstallationRetiresPREnvironment(t *testing.T) {
	store, ctx := pgStore(t)
	f := seedPREnvironment(t, store)
	if err := store.RevokeGitHubInstallation(ctx, f.installation); err != nil {
		t.Fatalf("RevokeGitHubInstallation: %v", err)
	}
	assertPreviewStates(t, store,
		previewStateWant{f.webPreview, state.PreviewPrStateStale},
		previewStateWant{f.workerPreview, state.PreviewPrStateStale},
		previewStateWant{f.otherRepoPreview, state.PreviewPrStateStale})
	if project, err := store.ProjectByID(ctx, f.project.ID); err != nil || project.InstallID != 0 || project.RepoFullName != f.repo {
		t.Fatalf("project after revoke = %+v, %v; want detached from the installation, repository kept", project, err)
	}

	reinstalled := f.installation + 1
	if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: f.web.ID, AccountID: f.acct.ID,
		BindingID: "rebind-" + f.web.ID, InstallID: reinstalled, RepoFullName: f.repo, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if project, err := store.ProjectByRepo(ctx, f.acct.ID, reinstalled, f.repo); err != nil || project.ID != f.project.ID {
		t.Fatalf("project after reconnecting its app = %+v, %v; want it on the new installation", project, err)
	}
}

// Removing a repository from the installation retires that repository's PR
// environments, every service of them, and leaves the others.
func TestPgStoreRemoveGitHubRepositoriesRetiresPREnvironment(t *testing.T) {
	store, ctx := pgStore(t)
	f := seedPREnvironment(t, store)
	if err := store.RemoveGitHubRepositories(ctx, f.installation, []string{f.repo}); err != nil {
		t.Fatalf("RemoveGitHubRepositories: %v", err)
	}
	assertPreviewStates(t, store,
		previewStateWant{f.webPreview, state.PreviewPrStateStale},
		previewStateWant{f.workerPreview, state.PreviewPrStateStale},
		previewStateWant{f.otherRepoPreview, state.PreviewPrStateOpen})
	if project, err := store.ProjectByID(ctx, f.project.ID); err != nil || project.InstallID != 0 {
		t.Fatalf("project after its repository was removed = %+v, %v; want detached", project, err)
	}
}
