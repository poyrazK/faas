// adr: 20261101160300
package githubd

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// appBoundPreviewService previews pull requests on octo/api for apps bound
// without a project, with source, scan and builds wired.
func appBoundPreviewService(t *testing.T, rig *testRig) (*Service, *previewDependencyEnqueuer) {
	t.Helper()
	if err := rig.mem.UpdateAccountPlan(context.Background(), rig.acct, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = memBindings{mem: rig.mem, acct: rig.acct}
	svc.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{
		rig.acct: {AccountID: rig.acct, InstallationID: rig.install},
	}}
	svc.Reconcile = rig.rec
	svc.Reconcile.Scan = func(fs.FS, reposcan.ScanOptions) (reposcan.Result, error) { return reposcan.Result{}, nil }
	svc.Source = &stubSource{fsys: fstest.MapFS{"index.js": &fstest.MapFile{Data: []byte("console.log(1)\n")}}}
	svc.WorkDir = t.TempDir()
	enqueuer := &previewDependencyEnqueuer{}
	svc.Enqueuer = enqueuer
	return svc, enqueuer
}

func previewOpen(t *testing.T, mem *state.MemStore, slug string) bool {
	t.Helper()
	preview, err := mem.AppBySlug(context.Background(), slug)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("preview %s: %v", slug, err)
	}
	return preview.PreviewPrState == state.PreviewPrStateOpen
}

// A pull request previews every app bound without a project to its branch,
// not only the first binding, and skips an app whose previews are off. Closing
// the PR closes them all.
func TestPullRequestPreviewsEveryAppBoundToBranch(t *testing.T) {
	ctx := context.Background()
	rig := newRig(t, nil)
	svc, enqueuer := appBoundPreviewService(t, rig)
	rig.bindApp(t, "web", "", "octo/api", "main")
	rig.bindApp(t, "worker", "", "octo/api", "main")
	quiet := rig.bindApp(t, "quiet", "", "octo/api", "main")
	off := false
	if err := rig.mem.UpdateGitHubAppPolicy(ctx, quiet.ID, rig.acct, state.GitHubAppPolicyPatch{PreviewEnabled: &off}); err != nil {
		t.Fatal(err)
	}

	head := strings.Repeat("a", 40)
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedOnBranches(9, head, "main", "main")); err != nil {
		t.Fatalf("open PR: %v", err)
	}
	for _, slug := range []string{"pr-9-web", "pr-9-worker"} {
		if !previewOpen(t, rig.mem, slug) {
			t.Fatalf("no open preview %s", slug)
		}
	}
	if previewOpen(t, rig.mem, "pr-9-quiet") {
		t.Fatal("previewed an app whose previews are off")
	}
	if len(enqueuer.specs) != 2 {
		t.Fatalf("enqueued %d preview builds, want 2", len(enqueuer.specs))
	}
	set, err := rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 9)
	if err != nil || len(set.MemberAppIDs) != 2 {
		t.Fatalf("preview set = %+v, %v; want both previews", set, err)
	}

	if _, err := svc.handlePullRequest(ctx, pullRequestClosedBody(9, head)); err != nil && !errors.Is(err, ErrIgnored) {
		t.Fatalf("close PR: %v", err)
	}
	for _, slug := range []string{"pr-9-web", "pr-9-worker"} {
		if previewOpen(t, rig.mem, slug) {
			t.Fatalf("preview %s still open after the PR closed", slug)
		}
	}
}

// With previews off for every app bound to the branch, a pull request builds
// nothing.
func TestPullRequestPreviewsOffForEveryBoundApp(t *testing.T) {
	ctx := context.Background()
	rig := newRig(t, nil)
	svc, enqueuer := appBoundPreviewService(t, rig)
	web := rig.bindApp(t, "web", "", "octo/api", "main")
	off := false
	if err := rig.mem.UpdateGitHubAppPolicy(ctx, web.ID, rig.acct, state.GitHubAppPolicyPatch{PreviewEnabled: &off}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.handlePullRequest(ctx, pullRequestOpenedOnBranches(10, strings.Repeat("b", 40), "main", "main"))
	if !errors.Is(err, ErrIgnored) {
		t.Fatalf("err = %v, want ErrIgnored", err)
	}
	if len(enqueuer.specs) != 0 || previewOpen(t, rig.mem, "pr-10-web") {
		t.Fatalf("built %d previews with previews off", len(enqueuer.specs))
	}
}
