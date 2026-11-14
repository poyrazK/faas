package githubd

import (
	"context"
	"fmt"
	"strings"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Branch-backed PR previews (ADR-20261113121342). A project on preview mode
// branch previews each pull request as environment branch pr-{N} of
// production, which apid owns: githubd asks for it over the bridge and
// builds the pull request's head of the production apps into its scope.
// Anything that keeps the branch from serving falls back to an app preview.

// PullRequestBranchClient is apid's bridge for branch-backed PR previews.
// cmd/githubd's live bridge client implements it; nil keeps app previews.
type PullRequestBranchClient interface {
	EnsurePullRequestBranch(ctx context.Context, in *githubdpb.EnsurePullRequestBranchRequest) (*githubdpb.EnsurePullRequestBranchResponse, error)
	ClosePullRequestBranch(ctx context.Context, in *githubdpb.ClosePullRequestBranchRequest) (*githubdpb.ClosePullRequestBranchResponse, error)
}

const branchPreviewUnavailable = "Branch previews are unavailable on this Gregale, so this pull request gets an app preview."

// branchPreview previews the pull request as an environment branch when its
// project chose them. handled=false leaves the event to the app preview
// path, with fallback saying why when the project chose branch previews.
func (s *Service) branchPreview(ctx context.Context, ev PullRequestEvent, target pullRequestTarget) (result reconcile.Result, handled bool, fallback string, err error) {
	if target.parent.ProjectID == "" || target.policy.PreviewMode != state.PreviewModeBranch {
		return reconcile.Result{}, false, "", nil
	}
	if s.PullRequestBranches == nil {
		return reconcile.Result{}, false, branchPreviewUnavailable, nil
	}
	if ev.Action == PullRequestActionClosed {
		// The app path still closes an app preview a fallback left.
		return reconcile.Result{}, false, "", s.closeBranchPreview(ctx, ev, target)
	}
	ttl := time.Duration(target.policy.PreviewTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = previewDefaultTTL
	}
	branch, err := s.PullRequestBranches.EnsurePullRequestBranch(ctx, &githubdpb.EnsurePullRequestBranchRequest{
		AccountId: target.parent.AccountID, ProjectId: target.parent.ProjectID,
		PullRequestNumber: int32(ev.Number), ExpiresAtUnix: time.Now().Add(ttl).Unix(),
	})
	switch {
	case status.Code(err) == codes.Unimplemented:
		return reconcile.Result{}, false, branchPreviewUnavailable, nil
	case err != nil:
		return reconcile.Result{}, true, "", fmt.Errorf("githubd: ensure PR branch: %w", err)
	case !branch.GetReady():
		return reconcile.Result{}, false, branch.GetFallbackReason(), nil
	}
	result, err = s.buildBranchPreview(ctx, ev, target, branch)
	return result, true, "", err
}

// branchPreviewURL is the parent app's stable URL in the branch, or the
// first workload's when the parent is not in it.
func branchPreviewURL(branch *githubdpb.EnsurePullRequestBranchResponse, parent state.App) string {
	for _, workload := range branch.GetWorkloads() {
		if workload.GetAppId() == parent.ID {
			return workload.GetUrl()
		}
	}
	if workloads := branch.GetWorkloads(); len(workloads) > 0 {
		return workloads[0].GetUrl()
	}
	return EnvironmentBranchWorkloadURL(parent.ProjectID, branch.GetEnvironment(), parent.ID)
}

func branchPreviewCommentMarker(environment string) string {
	return "<!-- gregale-preview-branch:" + environment + " -->"
}

func branchPreviewCommentBody(environment, previewURL, commitSHA, status, detail string) string {
	return fmt.Sprintf("%s\n### Gregale branch preview — %s\n\n%s\n\n[Open preview](%s)\n\nEnvironment: `%s` (a branch of production)\nCommit: `%s`",
		branchPreviewCommentMarker(environment), status, detail, previewURL, environment, commitSHA)
}

// buildBranchPreview builds the pull request's head of the previewed
// production apps into the branch.
func (s *Service) buildBranchPreview(ctx context.Context, ev PullRequestEvent, target pullRequestTarget, branch *githubdpb.EnsurePullRequestBranchResponse) (reconcile.Result, error) {
	environment, parent := branch.GetEnvironment(), target.parent
	previewURL := branchPreviewURL(branch, parent)
	installationID, repo, sha := target.install.InstallationID, ev.Repository.FullName, ev.PullRequest.HeadSHA
	detail := "The pull request's builds deploy into its environment branch. It keeps its data across pushes; reset it with `gregale environment branch` to copy production again."
	if werr := s.writePreviewComment(ctx, installationID, repo, ev.Number, branchPreviewCommentMarker(environment),
		branchPreviewCommentBody(environment, previewURL, sha, "deploying", detail)); werr != nil {
		s.Log.Warn("githubd: upsert branch preview comment", "err", werr, "repo", repo, "pr", ev.Number, "environment", environment)
	}
	if s.Source == nil || s.Enqueuer == nil || s.WorkDir == "" {
		return reconcile.Result{}, nil
	}
	tree, err := s.Source.Fetch(ctx, target.binding.AccountID, installationID, repo, sha)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: fetch branch preview source: %w", err)
	}
	defer func() { _ = tree.Close() }()
	scan, err := s.Reconcile.Scan(tree.FS(), s.scanOptions(s.capabilitySubject(ctx, target.binding.AccountID, parent.OrgID)))
	if err != nil {
		return reconcile.Result{}, previewSetupFailure("Gregale could not read the project at this commit: %s", strings.TrimPrefix(err.Error(), "reposcan: "))
	}
	dependencies, err := s.previewDependencyParents(ctx, parent, scan, ev.Number)
	if err != nil {
		return reconcile.Result{}, err
	}
	// Fetching and scanning can outlast a head update; build only the head.
	if current, currentErr := s.currentPullRequestDelivery(ctx, installationID, &ev); currentErr != nil {
		return reconcile.Result{}, fmt.Errorf("githubd: recheck current PR state: %w", currentErr)
	} else if !current {
		return reconcile.Result{WasIgnored: true}, ErrIgnored
	}
	if webhookDeliveryID(ctx) == "" {
		if werr := s.writePreviewCheck(ctx, installationID, repo, sha, githubdgrpc.CheckPhaseQueued, previewURL,
			fmt.Sprintf("Branch preview %s of production for PR #%d", environment, ev.Number)); werr != nil {
			s.Log.Warn("githubd: write queued branch preview check", "err", werr, "repo", repo, "sha", sha)
		}
	}
	project := state.Project{AccountID: target.binding.AccountID, RepoFullName: repo, ProductionBranch: ev.PullRequest.HeadRef}
	var result reconcile.Result
	for _, app := range append([]state.App{parent}, dependencies...) {
		sourcePath, sourceBytes, sourceURL, stageErr := s.stageAppSource(ctx, tree, app, project, sha, ev.PullRequest.HeadRef)
		if stageErr != nil {
			return result, fmt.Errorf("githubd: stage branch preview source for %q: %w", app.WorkloadName, stageErr)
		}
		build, enqueueErr := s.Enqueuer.Enqueue(ctx, BuildSpec{
			App: app, DeliveryID: webhookDeliveryID(ctx), CommitSHA: sha, RepoFullName: repo,
			Ref: "refs/heads/" + ev.PullRequest.HeadRef, Branch: ev.PullRequest.HeadRef, Scope: environment,
			Pusher: ev.Sender.Login, SourcePath: sourcePath, SourceURL: sourceURL, SourceBytes: sourceBytes,
			PRNumber: int32(ev.Number), SenderLogin: ev.Sender.Login,
			EventKind: githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST, EnvironmentBranch: true,
		})
		if enqueueErr != nil {
			if setup := enqueueSetupFailure(app.WorkloadName, enqueueErr); setup != nil {
				return result, setup
			}
			return result, fmt.Errorf("githubd: enqueue branch preview build for %q: %w", app.WorkloadName, enqueueErr)
		}
		if build.ID != "" {
			result.BuildIDs = append(result.BuildIDs, build.ID)
		}
	}
	return result, nil
}

// closeBranchPreview starts the closed pull request's branch grace and says
// so on its comment.
func (s *Service) closeBranchPreview(ctx context.Context, ev PullRequestEvent, target pullRequestTarget) error {
	closed, err := s.PullRequestBranches.ClosePullRequestBranch(ctx, &githubdpb.ClosePullRequestBranchRequest{
		AccountId: target.parent.AccountID, ProjectId: target.parent.ProjectID,
		PullRequestNumber: int32(ev.Number), ExpiresAtUnix: time.Now().Add(state.PRPreviewClosedGrace).Unix(),
	})
	switch {
	case status.Code(err) == codes.Unimplemented:
		return nil
	case err != nil:
		return fmt.Errorf("githubd: close PR branch: %w", err)
	case !closed.GetClosed():
		return nil
	}
	environment := fmt.Sprintf("pr-%d", ev.Number)
	previewURL := EnvironmentBranchWorkloadURL(target.parent.ProjectID, environment, target.parent.ID)
	if werr := s.writePreviewComment(ctx, target.install.InstallationID, ev.Repository.FullName, ev.Number,
		branchPreviewCommentMarker(environment), branchPreviewCommentBody(environment, previewURL, ev.PullRequest.HeadSHA, "closed",
			"The PR is closed. The branch keeps serving for 24 hours, then it is deleted with its data.")); werr != nil {
		s.Log.Warn("githubd: close branch preview comment", "err", werr, "repo", ev.Repository.FullName, "pr", ev.Number)
	}
	return nil
}
