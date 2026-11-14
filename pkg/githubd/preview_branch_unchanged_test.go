// adr: 20261113121342
package githubd

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A push to a branch-previewed pull request rebuilds only the apps whose
// files changed since the build their branch serves; a shared file, a
// branch deployment copied from production, or an opened pull request
// rebuilds them all.
func TestHandlePullRequest_BranchPreviewRebuildsOnlyChangedApps(t *testing.T) {
	headA, headB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tc := range []struct {
		name      string
		body      []byte
		changed   []string
		workerPR  int
		wantBuilt []string
	}{
		{name: "api-only change", body: pullRequestSyncBody(42, headB), changed: []string{"api/index.ts"}, workerPR: 42, wantBuilt: []string{"api"}},
		{name: "worker-only change", body: pullRequestSyncBody(42, headB), changed: []string{"worker/worker.js"}, workerPR: 42, wantBuilt: []string{"worker"}},
		{name: "shared file", body: pullRequestSyncBody(42, headB), changed: []string{"package-lock.json"}, workerPR: 42, wantBuilt: []string{"api", "worker"}},
		{name: "worker copied from production", body: pullRequestSyncBody(42, headB), changed: []string{"api/index.ts"}, wantBuilt: []string{"api", "worker"}},
		{name: "reopened", body: pullRequestOpenedBody(42, headB), changed: []string{"api/index.ts"}, workerPR: 42, wantBuilt: []string{"api", "worker"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rig := newPreviewRig(t)
			policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
			policy.PreviewMode = state.PreviewModeBranch
			if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			apiDir := "api"
			if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{RootDir: &apiDir}); err != nil {
				t.Fatal(err)
			}
			worker, err := rig.mem.CreateApp(ctx, state.App{AccountID: rig.acct, ProjectID: rig.parentProjectID, Slug: "worker",
				WorkloadName: "worker", RootDir: "worker", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := rig.mem.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
				AccountID: rig.acct, ProjectID: rig.parentProjectID, ParentSlug: "production", Slug: "pr-42", PullRequestNumber: 42,
				ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
			}, api.MustLimitsFor(api.PlanPro)); err != nil {
				t.Fatal(err)
			}
			for _, seed := range []struct {
				appID string
				pr    int
			}{{rig.parentID, 42}, {worker.ID, tc.workerPR}} {
				kind := state.DeploymentKindPreview
				if seed.pr == 0 {
					kind = state.DeploymentKindGitHub
				}
				if _, err := rig.mem.CreateDeployment(ctx, state.Deployment{AppID: seed.appID, Scope: "pr-42", Kind: kind,
					CommitSHA: headA, PRNumber: seed.pr, Status: state.DeployLive}); err != nil {
					t.Fatal(err)
				}
			}
			svc, _ := newPreviewService(t, rig)
			svc.PullRequestBranches = &fakePullRequestBranches{ensure: &githubdpb.EnsurePullRequestBranchResponse{Ready: true, Environment: "pr-42"}}
			svc.Source = &stubSource{fsys: fstest.MapFS{"api/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")}, "worker/Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")}}}
			svc.WorkDir = t.TempDir()
			enqueuer := &previewDependencyEnqueuer{}
			svc.Enqueuer = enqueuer
			changed := &stubChangedFiles{files: tc.changed}
			svc.ChangedFiles = changed
			svc.Reconcile.Scan = func(fs.FS, reposcan.ScanOptions) (reposcan.Result, error) {
				return reposcan.Result{Workloads: []reposcan.Workload{
					{Name: "api", RootDir: "api", DependsOn: []string{"worker"}},
					{Name: "worker", RootDir: "worker"},
				}}, nil
			}
			if _, err := svc.handlePullRequest(ctx, tc.body); err != nil {
				t.Fatalf("handle PR: %v", err)
			}
			var built []string
			for _, spec := range enqueuer.specs {
				if spec.Scope != "pr-42" || !spec.EnvironmentBranch {
					t.Fatalf("build %+v is not a branch build", spec)
				}
				built = append(built, spec.App.WorkloadName)
			}
			slices.Sort(built)
			if !slices.Equal(built, tc.wantBuilt) {
				t.Fatalf("built %v, want %v", built, tc.wantBuilt)
			}
			if changed.calls > 0 && (changed.lastBase != headA || changed.lastHead != headB) {
				t.Fatalf("compared %s..%s, want the served build's commit against the head", changed.lastBase, changed.lastHead)
			}
		})
	}
}
