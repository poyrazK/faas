// adr: 20261101160300
package githubd

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// memBindings serves the dispatch lookup and the account listing from a
// MemStore, as the production state adapter does from Postgres. acct is the
// one account the repository listing covers.
type memBindings struct {
	mem  *state.MemStore
	acct string
}

func (b memBindings) GetAppBinding(ctx context.Context, repo, branch string) (state.GitHubBinding, error) {
	return b.mem.GithubInstallBindingForRepoBranch(ctx, repo, branch)
}

func (b memBindings) ListForAccount(ctx context.Context, accountID string) (map[string]state.GitHubBinding, error) {
	return b.mem.ListGithubInstallBindingsForAccount(ctx, accountID)
}

func (b memBindings) ListForInstallationRepo(ctx context.Context, installationID int64, repo string) ([]state.GitHubBinding, error) {
	all, err := b.mem.ListGithubInstallBindingsForAccount(ctx, b.acct)
	if err != nil {
		return nil, err
	}
	var out []state.GitHubBinding
	for appID, binding := range all {
		if binding.InstallID == installationID && binding.RepoFullName == repo {
			binding.AppID = appID
			out = append(out, binding)
		}
	}
	return out, nil
}

// appBoundRig is a push rig with no project bound to octo/api. Scanning fails
// the test: a push to apps bound without a project has nothing to reconcile.
func appBoundRig(t *testing.T) (*Service, *recordingEnqueuer, *testRig) {
	t.Helper()
	rig := newRig(t, func(fs.FS) (reposcan.Result, error) {
		t.Error("scan called for a push to apps bound without a project")
		return reposcan.Result{}, nil
	})
	svc := newServiceForRig(t, rig)
	svc.Bindings = memBindings{mem: rig.mem, acct: rig.acct}
	rec := &recordingEnqueuer{}
	svc.Enqueuer = rec
	return svc, rec, rig
}

func (r *testRig) bindApp(t *testing.T, slug, projectID, repo, branch string) state.App {
	t.Helper()
	ctx := context.Background()
	app, err := r.mem.CreateApp(ctx, state.App{
		AccountID: r.acct, ProjectID: projectID, Slug: slug, WorkloadName: slug,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("create %s: %v", slug, err)
	}
	if err := r.mem.UpsertGithubInstallBinding(ctx, state.GitHubBinding{
		AppID: app.ID, AccountID: r.acct, BindingID: "bind-" + app.ID,
		InstallID: r.install, RepoFullName: repo, ProductionBranch: branch,
	}); err != nil {
		t.Fatalf("bind %s: %v", slug, err)
	}
	return app
}

const appBoundPush = `{"ref":"refs/heads/main","before":"base123","after":"head456","installation":{"id":42},"repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`

func enqueuedApps(rec *recordingEnqueuer) []string {
	ids := make([]string, 0, len(rec.calls))
	for _, call := range rec.calls {
		ids = append(ids, call.appID)
	}
	sort.Strings(ids)
	return ids
}

// The acceptance test for github-deploys: an app bound with the wizard,
// `gregale github bind` or the bind API deploys on a push to its production
// branch, with no project bound to the repository.
func TestPushDeploysAppBoundWithoutProject(t *testing.T) {
	svc, rec, rig := appBoundRig(t)
	api := rig.bindApp(t, "api", "", "octo/api", "main")
	worker := rig.bindApp(t, "worker", "", "octo/api", "main")
	rig.bindApp(t, "staging", "", "octo/api", "release")

	if _, err := svc.HandlePushRequest(context.Background(), []byte(appBoundPush)); err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	want := []string{api.ID, worker.ID}
	sort.Strings(want)
	if got := enqueuedApps(rec); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("enqueued %v, want the two apps bound to main %v", got, want)
	}
	for _, call := range rec.calls {
		if call.commitSHA != "head456" || call.githubSourceRef != "main" || call.githubInstallation != rig.install || call.sourcePath == "" {
			t.Fatalf("build spec = %+v", call)
		}
	}
}

func TestPushToAppsBoundWithoutProjectHonorsActionsTrigger(t *testing.T) {
	svc, rec, rig := appBoundRig(t)
	ctx := context.Background()
	api := rig.bindApp(t, "api", "", "octo/api", "main")
	worker := rig.bindApp(t, "worker", "", "octo/api", "main")
	if err := rig.mem.SetGitHubAppProductionTrigger(ctx, worker.ID, rig.acct, state.ProductionTriggerActions); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HandlePushRequest(ctx, []byte(appBoundPush)); err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if got := enqueuedApps(rec); len(got) != 1 || got[0] != api.ID {
		t.Fatalf("enqueued %v, want only the webhook-triggered app", got)
	}

	if err := rig.mem.SetGitHubAppProductionTrigger(ctx, api.ID, rig.acct, state.ProductionTriggerActions); err != nil {
		t.Fatal(err)
	}
	rec.calls = nil
	if _, err := svc.HandlePushRequest(ctx, []byte(appBoundPush)); !errors.Is(err, ErrIgnored) {
		t.Fatalf("every app on Actions: err = %v, want ErrIgnored", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("enqueued %d builds although GitHub Actions deploys every bound app", len(rec.calls))
	}
}

func TestPushSkipsDeletedAndForeignProjectBoundApps(t *testing.T) {
	svc, rec, rig := appBoundRig(t)
	ctx := context.Background()
	deleted := rig.bindApp(t, "gone", "", "octo/api", "main")
	if err := rig.mem.DeleteApp(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	other, err := rig.mem.CreateProject(ctx, state.Project{
		AccountID: rig.acct, Slug: "other", InstallID: rig.install,
		RepoFullName: "octo/other", ProductionBranch: "main", ScanSource: state.ProjectScanSource("single"),
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.bindApp(t, "member", other.ID, "octo/api", "main")

	if _, err := svc.HandlePushRequest(ctx, []byte(appBoundPush)); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("err = %v, want ErrNoBinding", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("enqueued %v; a deleted app and an app whose project owns another repository must not build", enqueuedApps(rec))
	}
}

// An app bound at the app level deploys beside the project bound to the same
// repository, to its own production scope; it used to be left out.
func TestPushDeploysAppBoundBesideProject(t *testing.T) {
	rig := newRig(t, func(fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	svc := newServiceForRig(t, rig)
	svc.Bindings = memBindings{mem: rig.mem, acct: rig.acct}
	rec := &recordingEnqueuer{}
	svc.Enqueuer = rec
	project := rig.seedProject(t, "octo/api", "main")
	worker := rig.bindApp(t, "worker", "", "octo/api", "main")

	if _, err := svc.HandlePushRequest(context.Background(), []byte(appBoundPush)); err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	members, err := rig.mem.AppsForProject(context.Background(), rig.acct, project.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("project apps = %+v, %v; want the reconciled workload", members, err)
	}
	want := []string{members[0].ID, worker.ID}
	sort.Strings(want)
	if got := enqueuedApps(rec); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("enqueued %v, want the project's app and the app-bound worker %v", got, want)
	}
	for _, call := range rec.calls {
		if call.appID == worker.ID && call.scope != state.DefaultEnvScope {
			t.Fatalf("worker deployed to scope %q, want %q", call.scope, state.DefaultEnvScope)
		}
	}
}

// A push to a branch the project does not deploy still deploys the apps bound
// to that branch, without reconciling the project.
func TestPushToBranchOutsideProjectDeploysAppBound(t *testing.T) {
	svc, rec, rig := appBoundRig(t)
	rig.seedProject(t, "octo/api", "main")
	worker := rig.bindApp(t, "worker", "", "octo/api", "develop")
	push := strings.Replace(appBoundPush, "refs/heads/main", "refs/heads/develop", 1)

	if _, err := svc.HandlePushRequest(context.Background(), []byte(push)); err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if got := enqueuedApps(rec); len(got) != 1 || got[0] != worker.ID {
		t.Fatalf("enqueued %v, want only the app bound to develop", got)
	}
}

// branchHeadsByName answers each branch's head.
type branchHeadsByName map[string]string

func (b branchHeadsByName) BranchHead(_ context.Context, _ int64, _, branch string) (string, error) {
	return b[branch], nil
}

const appBoundTag = `{"ref":"refs/tags/v1.2.0","before":"0000000000000000000000000000000000000000","after":"head456","created":true,"installation":{"id":42},"repository":{"full_name":"octo/api","name":"api","default_branch":"main"},"pusher":{"name":"alice"}}`

// A release tag deploys the apps bound without a project to the branch whose
// head it tags, whether or not that is the repository's default branch.
func TestReleaseTagDeploysAppsBoundToTaggedBranch(t *testing.T) {
	t.Run("a branch other than the default", func(t *testing.T) {
		svc, rec, rig := appBoundRig(t)
		rig.bindApp(t, "web", "", "octo/api", "main")
		release := rig.bindApp(t, "release", "", "octo/api", "release")
		svc.BranchHeads = branchHeadsByName{"main": "other789", "release": "head456"}
		if _, err := svc.HandlePushRequest(context.Background(), []byte(appBoundTag)); err != nil {
			t.Fatalf("HandlePushRequest: %v", err)
		}
		if got := enqueuedApps(rec); len(got) != 1 || got[0] != release.ID {
			t.Fatalf("enqueued %v, want only the app bound to the tagged release branch", got)
		}
	})
	t.Run("no binding on the default branch", func(t *testing.T) {
		svc, rec, rig := appBoundRig(t)
		release := rig.bindApp(t, "release", "", "octo/api", "release")
		svc.BranchHeads = branchHeadsByName{"release": "head456"}
		if _, err := svc.HandlePushRequest(context.Background(), []byte(appBoundTag)); err != nil {
			t.Fatalf("HandlePushRequest: %v", err)
		}
		if got := enqueuedApps(rec); len(got) != 1 || got[0] != release.ID {
			t.Fatalf("enqueued %v, want the app bound to release", got)
		}
	})
	t.Run("a commit at no bound branch head", func(t *testing.T) {
		svc, rec, rig := appBoundRig(t)
		rig.bindApp(t, "web", "", "octo/api", "main")
		svc.BranchHeads = branchHeadsByName{"main": "other789"}
		if _, err := svc.HandlePushRequest(context.Background(), []byte(appBoundTag)); !errors.Is(err, ErrIgnored) {
			t.Fatalf("err = %v, want ErrIgnored", err)
		}
		if len(rec.calls) != 0 {
			t.Fatalf("enqueued %v for a tag that is not a production head", enqueuedApps(rec))
		}
	})
}
