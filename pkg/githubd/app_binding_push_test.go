// adr: 20261101160300
package githubd

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// memBindings serves the dispatch lookup and the account listing from a
// MemStore, as the production state adapter does from Postgres.
type memBindings struct{ mem *state.MemStore }

func (b memBindings) GetAppBinding(ctx context.Context, repo, branch string) (state.GitHubBinding, error) {
	return b.mem.GithubInstallBindingForRepoBranch(ctx, repo, branch)
}

func (b memBindings) ListForAccount(ctx context.Context, accountID string) (map[string]state.GitHubBinding, error) {
	return b.mem.ListGithubInstallBindingsForAccount(ctx, accountID)
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
	svc.Bindings = memBindings{mem: rig.mem}
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
