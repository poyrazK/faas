package githubd

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakePullRequestBranches is apid's bridge for branch previews.
type fakePullRequestBranches struct {
	ensure    *githubdpb.EnsurePullRequestBranchResponse
	ensureErr error
	closed    bool
	ensured   []*githubdpb.EnsurePullRequestBranchRequest
	closes    []*githubdpb.ClosePullRequestBranchRequest
}

func (f *fakePullRequestBranches) EnsurePullRequestBranch(_ context.Context, in *githubdpb.EnsurePullRequestBranchRequest) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	f.ensured = append(f.ensured, in)
	return f.ensure, f.ensureErr
}

func (f *fakePullRequestBranches) ClosePullRequestBranch(_ context.Context, in *githubdpb.ClosePullRequestBranchRequest) (*githubdpb.ClosePullRequestBranchResponse, error) {
	f.closes = append(f.closes, in)
	return &githubdpb.ClosePullRequestBranchResponse{Closed: f.closed}, nil
}

// ADR-20261113121342: a project on branch previews gets its pull request's
// head built into environment branch pr-{N} of production; any reason the
// branch cannot serve falls back to an app preview, named on the check run.
func TestHandlePullRequest_BranchPreviews(t *testing.T) {
	const branchURL = "https://env-slot-app.gregale.dev"
	ready := &githubdpb.EnsurePullRequestBranchResponse{Ready: true, Environment: "pr-42", Created: true}
	for _, tc := range []struct {
		name       string
		mode       state.PreviewMode
		branches   *fakePullRequestBranches
		wantBranch bool
		wantReason string
	}{
		{name: "app mode", mode: state.PreviewModeApp, branches: &fakePullRequestBranches{ensure: ready}},
		{name: "branch mode", mode: state.PreviewModeBranch, branches: &fakePullRequestBranches{ensure: ready}, wantBranch: true},
		{name: "branch limit", mode: state.PreviewModeBranch,
			branches:   &fakePullRequestBranches{ensure: &githubdpb.EnsurePullRequestBranchResponse{FallbackReason: "limit 5, observed 5, so it gets an app preview."}},
			wantReason: "limit 5, observed 5"},
		{name: "older apid", mode: state.PreviewModeBranch,
			branches:   &fakePullRequestBranches{ensureErr: status.Error(codes.Unimplemented, "unknown method")},
			wantReason: branchPreviewUnavailable},
		{name: "no bridge", mode: state.PreviewModeBranch, wantReason: branchPreviewUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rig := newPreviewRig(t)
			policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
			policy.PreviewMode = tc.mode
			if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			svc, rec := newPreviewService(t, rig)
			svc.WritePreviewCommentForInstallation = rec.writePreviewComment
			if tc.branches != nil {
				if tc.branches.ensure != nil && tc.branches.ensure.Ready {
					tc.branches.ensure.Workloads = []*githubdpb.PullRequestBranchWorkload{{AppId: rig.parentID, Workload: rig.parentSlug, Url: branchURL}}
				}
				svc.PullRequestBranches = tc.branches
			}
			svc.Source = &stubSource{fsys: fstest.MapFS{"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch\n")}}}
			svc.WorkDir = t.TempDir()
			enqueuer := &previewDependencyEnqueuer{}
			svc.Enqueuer = enqueuer
			svc.Reconcile.Scan = func(fs.FS, reposcan.ScanOptions) (reposcan.Result, error) {
				return reposcan.Result{Workloads: []reposcan.Workload{{Name: rig.parentWorkloadName, Dockerfile: "Dockerfile"}}}, nil
			}
			if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40))); err != nil {
				t.Fatalf("open PR: %v", err)
			}
			_, previewErr := rig.mem.AppBySlug(ctx, "pr-42-"+rig.parentSlug)
			if tc.wantBranch {
				if !errors.Is(previewErr, state.ErrNotFound) {
					t.Fatalf("app preview = %v; want none for a branch preview", previewErr)
				}
				if len(enqueuer.specs) != 1 {
					t.Fatalf("builds = %+v; want the parent's head into the branch", enqueuer.specs)
				}
				spec := enqueuer.specs[0]
				if spec.App.ID != rig.parentID || spec.Scope != "pr-42" || !spec.EnvironmentBranch || spec.PRNumber != 42 ||
					spec.EventKind != githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST {
					t.Fatalf("build = %+v; want production app %s into pr-42", spec, rig.parentID)
				}
				asked := tc.branches.ensured[0]
				if asked.GetProjectId() != rig.parentProjectID || asked.GetPullRequestNumber() != 42 ||
					time.Until(time.Unix(asked.GetExpiresAtUnix(), 0)) < 6*24*time.Hour {
					t.Fatalf("ensure request = %+v; want the project's PR 42 for the preview TTL", asked)
				}
				if len(rec.comments) == 0 || rec.comments[0].marker != branchPreviewCommentMarker("pr-42") || !strings.Contains(rec.comments[0].body, branchURL) {
					t.Fatalf("comments = %+v; want the branch comment with %s", rec.comments, branchURL)
				}
				return
			}
			if previewErr != nil {
				t.Fatalf("app preview: %v; want one", previewErr)
			}
			for _, spec := range enqueuer.specs {
				if spec.EnvironmentBranch {
					t.Fatalf("build %+v went into a branch; want an app preview build", spec)
				}
			}
			if tc.mode == state.PreviewModeApp && len(tc.branches.ensured) != 0 {
				t.Fatalf("app mode asked apid for a branch: %+v", tc.branches.ensured)
			}
			if tc.wantReason != "" {
				found := false
				for _, comment := range rec.comments {
					found = found || strings.Contains(comment.body, tc.wantReason)
				}
				if !found {
					t.Fatalf("comments %+v do not say why: %q", rec.comments, tc.wantReason)
				}
			}
		})
	}
}

// Closing a pull request starts its branch's grace and says so.
func TestHandlePullRequest_BranchPreviewClose(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	policy.PreviewMode = state.PreviewModeBranch
	if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	svc, rec := newPreviewService(t, rig)
	svc.WritePreviewCommentForInstallation = rec.writePreviewComment
	branches := &fakePullRequestBranches{closed: true}
	svc.PullRequestBranches = branches
	if _, err := svc.handlePullRequest(ctx, pullRequestClosedBody(42, strings.Repeat("b", 40))); err != nil {
		t.Fatalf("close PR: %v", err)
	}
	if len(branches.closes) != 1 || branches.closes[0].GetPullRequestNumber() != 42 ||
		time.Until(time.Unix(branches.closes[0].GetExpiresAtUnix(), 0)) > state.PRPreviewClosedGrace {
		t.Fatalf("closes = %+v; want PR 42 closed with the grace", branches.closes)
	}
	if len(rec.comments) != 1 || rec.comments[0].marker != branchPreviewCommentMarker("pr-42") || !strings.Contains(rec.comments[0].body, "closed") {
		t.Fatalf("comments = %+v; want the branch comment closed", rec.comments)
	}
}
