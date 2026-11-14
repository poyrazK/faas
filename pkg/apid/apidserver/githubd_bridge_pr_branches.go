package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Branch-backed PR previews (ADR-20261113121342). githubd asks apid, which
// owns environment branches, for a pull request's branch over the bridge;
// apid answers with the branch or a reason the pull request gets an app
// preview instead.

// pullRequestBranches is the bridge's view of branch-backed PR previews;
// *server implements it.
type pullRequestBranches interface {
	ensurePullRequestBranch(ctx context.Context, req pullRequestBranchRequest) (*githubdpb.EnsurePullRequestBranchResponse, error)
	closePullRequestBranch(ctx context.Context, req pullRequestBranchRequest) (bool, error)
	pullRequestBranchBuild(ctx context.Context, app state.App, scope string, pullRequest int) error
}

// pullRequestBranchRequest is a validated Ensure or Close request.
type pullRequestBranchRequest struct {
	accountID, projectID string
	pullRequest          int
	expiresAt            time.Time
}

// pullRequestBranchSlug is the environment a pull request's branch is.
func pullRequestBranchSlug(pullRequest int) string {
	return fmt.Sprintf("pr-%d", pullRequest)
}

func newPullRequestBranchRequest(accountID, projectID string, pullRequest int32, expiresAtUnix int64) (pullRequestBranchRequest, error) {
	if accountID == "" || projectID == "" || pullRequest <= 0 || expiresAtUnix <= 0 {
		return pullRequestBranchRequest{}, status.Error(codes.InvalidArgument, "account_id, project_id, pull_request_number and expires_at_unix are required")
	}
	return pullRequestBranchRequest{accountID: accountID, projectID: projectID, pullRequest: int(pullRequest),
		expiresAt: time.Unix(expiresAtUnix, 0).UTC()}, nil
}

// EnsurePullRequestBranch makes or refreshes a pull request's branch.
func (g *githubdBridge) EnsurePullRequestBranch(ctx context.Context, in *githubdpb.EnsurePullRequestBranchRequest) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	if g.prBranches == nil {
		return nil, status.Error(codes.Unimplemented, "EnsurePullRequestBranch: branch previews are not wired")
	}
	req, err := newPullRequestBranchRequest(in.GetAccountId(), in.GetProjectId(), in.GetPullRequestNumber(), in.GetExpiresAtUnix())
	if err != nil {
		return nil, err
	}
	return g.prBranches.ensurePullRequestBranch(ctx, req)
}

// ClosePullRequestBranch starts a closed pull request's branch grace.
func (g *githubdBridge) ClosePullRequestBranch(ctx context.Context, in *githubdpb.ClosePullRequestBranchRequest) (*githubdpb.ClosePullRequestBranchResponse, error) {
	if g.prBranches == nil {
		return nil, status.Error(codes.Unimplemented, "ClosePullRequestBranch: branch previews are not wired")
	}
	req, err := newPullRequestBranchRequest(in.GetAccountId(), in.GetProjectId(), in.GetPullRequestNumber(), in.GetExpiresAtUnix())
	if err != nil {
		return nil, err
	}
	closed, err := g.prBranches.closePullRequestBranch(ctx, req)
	if err != nil {
		return nil, err
	}
	return &githubdpb.ClosePullRequestBranchResponse{Closed: closed}, nil
}

// pullRequestBranchFallback answers ready=false: the pull request gets an
// app preview, and reason says why on its check run.
func pullRequestBranchFallback(reason string) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	return &githubdpb.EnsurePullRequestBranchResponse{FallbackReason: reason}, nil
}

// pullRequestBranchProject loads the project a request names, owned by the
// request's account, with the account its environment work runs as.
func (s *server) pullRequestBranchProject(ctx context.Context, req pullRequestBranchRequest) (context.Context, state.Project, state.Account, error) {
	project, err := s.store.ProjectByID(ctx, req.projectID)
	if err != nil || project.AccountID != req.accountID {
		if err == nil || errors.Is(err, state.ErrNotFound) {
			return ctx, state.Project{}, state.Account{}, status.Errorf(codes.NotFound, "project %q not found in account %q", req.projectID, req.accountID)
		}
		return ctx, state.Project{}, state.Account{}, status.Errorf(codes.Unavailable, "project lookup: %v", err)
	}
	ctx, acct, err := s.environmentProjectOwner(ctx, project)
	if err != nil {
		return ctx, state.Project{}, state.Account{}, status.Errorf(codes.Unavailable, "project owner: %v", err)
	}
	return ctx, project, acct, nil
}

// pullRequestBranchesOn reports whether the project's pull requests are
// previewed as branches, or why not.
func (s *server) pullRequestBranchesOn(ctx context.Context, project state.Project) (string, error) {
	if !s.environmentBranchPRPreviewsEnabled() {
		return "Branch previews are not enabled on this platform, so this pull request gets an app preview.", nil
	}
	policies, ok := s.store.(state.GitHubDeployPolicyStore)
	if !ok {
		return "Branch previews are unavailable, so this pull request gets an app preview.", nil
	}
	policy, err := policies.GetGitHubDeployPolicy(ctx, project.ID, project.AccountID)
	if err != nil {
		return "", status.Errorf(codes.Unavailable, "GitHub deployment policy: %v", err)
	}
	if policy.PreviewMode != state.PreviewModeBranch {
		return "The project previews pull requests as apps.", nil
	}
	return "", nil
}

func (s *server) ensurePullRequestBranch(ctx context.Context, req pullRequestBranchRequest) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	ctx, project, acct, err := s.pullRequestBranchProject(ctx, req)
	if err != nil {
		return nil, err
	}
	if reason, err := s.pullRequestBranchesOn(ctx, project); err != nil || reason != "" {
		if err != nil {
			return nil, err
		}
		return pullRequestBranchFallback(reason)
	}
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	extender, canExtend := s.store.(state.ProjectEnvironmentBranchExtender)
	if !ok || !canExtend || s.notif == nil {
		return pullRequestBranchFallback("Branch previews are unavailable, so this pull request gets an app preview.")
	}
	slug := pullRequestBranchSlug(req.pullRequest)
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, slug)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return s.createPullRequestBranch(ctx, store, acct, project, req)
	case err != nil:
		return nil, status.Errorf(codes.Unavailable, "branch lookup: %v", err)
	case branch.PullRequestNumber != req.pullRequest:
		return pullRequestBranchFallback(fmt.Sprintf("Environment %s already exists in the project and was not made for this pull request, so it gets an app preview.", slug))
	}
	// A branch still copying its data keeps its expiry; the next push moves it.
	if branch.DataState != state.EnvironmentBranchDataPending && req.expiresAt.After(branch.ExpiresAt) {
		moved, problem := s.moveEnvironmentBranchExpiry(ctx, extender, acct, project, branch, req.expiresAt, true)
		if problem != nil && problem.Status >= http.StatusInternalServerError {
			return nil, status.Errorf(codes.Unavailable, "branch expiry: %s", problem.Detail)
		}
		if problem == nil {
			branch = moved
		}
	}
	return s.pullRequestBranchReady(ctx, store, acct, project, branch, false)
}

func (s *server) createPullRequestBranch(ctx context.Context, store state.ProjectEnvironmentBranchStore, acct state.Account, project state.Project, req pullRequestBranchRequest) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	ttl := time.Until(req.expiresAt)
	ttl = max(ttl, api.EnvironmentBranchMinTTL)
	ttl = min(ttl, api.EnvironmentBranchMaxTTL)
	response, problem := s.branchProjectEnvironment(ctx, store, acct, project, environmentBranchSpec{
		parent: "production", slug: pullRequestBranchSlug(req.pullRequest), ttl: ttl,
		egress: api.EnvironmentBranchEgressLive, pullRequest: req.pullRequest,
	})
	if problem != nil {
		if problem.Status >= http.StatusInternalServerError {
			return nil, status.Errorf(codes.Unavailable, "create branch: %s", problem.Detail)
		}
		return pullRequestBranchFallback(pullRequestBranchProblemReason(problem))
	}
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, response.Slug)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "branch lookup: %v", err)
	}
	return s.pullRequestBranchReady(ctx, store, acct, project, branch, true)
}

// pullRequestBranchProblemReason is a create refusal as a check run line;
// a limit refusal names the limit and the observed count.
func pullRequestBranchProblemReason(problem *api.Problem) string {
	reason := "The pull request's environment branch could not be made (" + problem.Detail + ")"
	if problem.Limit != nil && problem.Observed != nil {
		reason += fmt.Sprintf("; limit %d, observed %d", *problem.Limit, *problem.Observed)
	}
	return reason + ", so it gets an app preview."
}

func (s *server) pullRequestBranchReady(ctx context.Context, store state.ProjectEnvironmentBranchStore, acct state.Account, project state.Project, branch state.ProjectEnvironmentBranch, created bool) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	parents, err := store.ProjectEnvironmentBranchWorkloads(ctx, acct.ID, project.ID, branch.Slug)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "branch workloads: %v", err)
	}
	out := &githubdpb.EnsurePullRequestBranchResponse{Ready: true, Environment: branch.Slug, Created: created}
	for _, parent := range parents {
		url, _ := environmentBranchWorkloadURLs(branch, parent.AppID)
		out.Workloads = append(out.Workloads, &githubdpb.PullRequestBranchWorkload{AppId: parent.AppID, Workload: parent.WorkloadSlug, Url: url})
	}
	return out, nil
}

func (s *server) closePullRequestBranch(ctx context.Context, req pullRequestBranchRequest) (bool, error) {
	ctx, project, acct, err := s.pullRequestBranchProject(ctx, req)
	if err != nil {
		return false, err
	}
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	extender, canExtend := s.store.(state.ProjectEnvironmentBranchExtender)
	if !ok || !canExtend {
		return false, nil
	}
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, pullRequestBranchSlug(req.pullRequest))
	switch {
	case errors.Is(err, state.ErrNotFound):
		return false, nil
	case err != nil:
		return false, status.Errorf(codes.Unavailable, "branch lookup: %v", err)
	case branch.PullRequestNumber != req.pullRequest:
		return false, nil
	case !branch.ExpiresAt.After(req.expiresAt):
		// Already ending at or before the grace: a repeated close keeps it.
		return true, nil
	case branch.DataState == state.EnvironmentBranchDataPending:
		return false, status.Error(codes.Unavailable, "the branch's data is still being copied; retry the close")
	}
	if _, problem := s.moveEnvironmentBranchExpiry(ctx, extender, acct, project, branch, req.expiresAt, true); problem != nil {
		return false, status.Errorf(codes.Unavailable, "close branch: %s", problem.Detail)
	}
	return true, nil
}

// pullRequestBranchBuild checks that scope is the pull request's own branch
// of the app's project, before a branch build is enqueued.
func (s *server) pullRequestBranchBuild(ctx context.Context, app state.App, scope string, pullRequest int) error {
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok || app.ProjectID == "" || pullRequest <= 0 || scope != pullRequestBranchSlug(pullRequest) {
		return status.Error(codes.FailedPrecondition, "EnqueueBuild: the build does not name its pull request's environment branch")
	}
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, app.AccountID, app.ProjectID, scope)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return status.Errorf(codes.FailedPrecondition, "EnqueueBuild: environment branch %s no longer exists", scope)
	case err != nil:
		return status.Errorf(codes.Unavailable, "EnqueueBuild: branch lookup: %v", err)
	case branch.PullRequestNumber != pullRequest:
		return status.Errorf(codes.FailedPrecondition, "EnqueueBuild: environment %s was not made for pull request #%d", scope, pullRequest)
	}
	return nil
}
