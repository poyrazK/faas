// adr: 20261108150000
package githubd

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func markPreviewsLive(t *testing.T, rig *previewTestRig, commit string, apps ...state.App) {
	t.Helper()
	for _, app := range apps {
		if _, err := rig.mem.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindPreview,
			CommitSHA: commit, Status: state.DeployLive}); err != nil {
			t.Fatal(err)
		}
	}
}

// A PR push rebuilds only the preview services whose files changed. The
// others stay on the build they serve, and the set counts them live at that
// build's commit. A shared file rebuilds everything again.
func TestHandlePullRequest_PushRebuildsOnlyChangedServices(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	if err := rig.mem.UpdateAccountPlan(ctx, rig.acct, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	apiDir := "api"
	if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{RootDir: &apiDir}); err != nil {
		t.Fatal(err)
	}
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{
		{Name: "api", RootDir: "api", DependsOn: []string{"worker"}},
		{Name: "worker", RootDir: "worker", Class: reposcan.ClassWorker, Command: []string{"node", "worker.js"}},
	})
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"compose.yaml":      &fstest.MapFile{Data: []byte("services: {}\n")},
		"api/index.ts":      &fstest.MapFile{Data: []byte("export {};\n")},
		"worker/worker.js":  &fstest.MapFile{Data: []byte("// worker\n")},
		"package-lock.json": &fstest.MapFile{Data: []byte("{}\n")},
	}}
	enqueuer := svc.Enqueuer.(*previewDependencyEnqueuer)
	headA, headB, headC := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, headA))
	if err != nil || len(opened.Added) != 2 || len(enqueuer.specs) != 2 {
		t.Fatalf("open = (%+v, %v), builds %d; want api and worker", opened, err, len(enqueuer.specs))
	}
	markPreviewsLive(t, rig, headA, opened.Added...)
	worker, err := rig.mem.AppBySlug(ctx, "pr-42-worker")
	if err != nil {
		t.Fatal(err)
	}

	changed := &stubChangedFiles{files: []string{"api/index.ts"}}
	svc.ChangedFiles = changed
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, headB)); err != nil {
		t.Fatal(err)
	}
	if len(enqueuer.specs) != 3 || enqueuer.specs[2].App.WorkloadName != "api" {
		t.Fatalf("builds after an api-only push = %d (last %q), want only api rebuilt", len(enqueuer.specs), enqueuer.specs[len(enqueuer.specs)-1].App.WorkloadName)
	}
	if changed.lastBase != headA || changed.lastHead != headB {
		t.Fatalf("compared %s..%s, want the previous head against the new head", changed.lastBase, changed.lastHead)
	}
	set, err := rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42)
	if err != nil || set.CommitSHA != headB || set.MemberCommit(worker.ID) != headA {
		t.Fatalf("set = (%+v, %v), want head b with worker pinned to a", set, err)
	}
	environment, err := rig.mem.PRPreviewEnvironmentByRoot(ctx, set.RootAppID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range environment.Members {
		want := previewset.PhaseLive
		if member.AppID != worker.ID {
			want = "missing" // api has not built head b yet
		}
		if member.DeploymentStatus != want {
			t.Fatalf("member %s = %q, want %q", member.WorkloadName, member.DeploymentStatus, want)
		}
	}

	// The api build at b goes live; a shared file then rebuilds both, and the
	// new head clears the pin.
	for _, app := range opened.Added {
		if app.ID != worker.ID {
			markPreviewsLive(t, rig, headB, app)
		}
	}
	changed.files = []string{"package-lock.json"}
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, headC)); err != nil {
		t.Fatal(err)
	}
	if len(enqueuer.specs) != 5 {
		t.Fatalf("builds after a shared-file push = %d, want both rebuilt", len(enqueuer.specs))
	}
	if set, err = rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42); err != nil || set.MemberCommit(worker.ID) != headC {
		t.Fatalf("set = (%+v, %v), want no pins on head c", set, err)
	}
}
