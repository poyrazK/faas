package apidserver

import (
	"context"
	"net/http"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 environment branches, for package apidserver_test.

const RuntimeConfigEnvironmentBranches = runtimeConfigEnvironmentBranches

// RuntimeConfigEnvironmentBranchPRPreviews is the branch PR previews flag
// (ADR-20261113121342).
const RuntimeConfigEnvironmentBranchPRPreviews = runtimeConfigEnvironmentBranchPRPreviews

// EnsurePullRequestBranch is the bridge's branch preview call.
func (r *server) EnsurePullRequestBranch(ctx context.Context, accountID, projectID string, pullRequest int, expiresAt time.Time) (*githubdpb.EnsurePullRequestBranchResponse, error) {
	return r.ensurePullRequestBranch(ctx, pullRequestBranchRequest{accountID: accountID, projectID: projectID, pullRequest: pullRequest, expiresAt: expiresAt})
}

// ClosePullRequestBranch is the bridge's closed pull request call.
func (r *server) ClosePullRequestBranch(ctx context.Context, accountID, projectID string, pullRequest int, expiresAt time.Time) (bool, error) {
	return r.closePullRequestBranch(ctx, pullRequestBranchRequest{accountID: accountID, projectID: projectID, pullRequest: pullRequest, expiresAt: expiresAt})
}

// PullRequestBranchBuild checks a branch build's scope.
func (r *server) PullRequestBranchBuild(ctx context.Context, app state.App, scope string, pullRequest int) error {
	return r.pullRequestBranchBuild(ctx, app, scope, pullRequest)
}

// PullRequestBranchBridge is a githubd bridge whose branch previews r
// serves, or none when served is false.
func (r *server) PullRequestBranchBridge(served bool) githubdpb.GithubdServer {
	bridge := &githubdBridge{log: r.log}
	if served {
		bridge.prBranches = r
	}
	return bridge
}

func (r *server) CreateProjectEnvironmentBranch(w http.ResponseWriter, req *http.Request, acct state.Account) {
	r.createProjectEnvironmentBranch(w, req, acct)
}

// ReconcileEnvironmentBranchData runs one pass of the branch data reconciler.
func (r *server) ReconcileEnvironmentBranchData(ctx context.Context, now time.Time) error {
	return r.reconcileEnvironmentBranchData(ctx, r.store.(state.ProjectEnvironmentBranchDataStore), now)
}

// EnvironmentBranchDatabaseTTL is the TTL a branch's database copy carries.
func EnvironmentBranchDatabaseTTL(branch state.ProjectEnvironmentBranch, point time.Time) time.Duration {
	return environmentBranchDatabaseTTL(branch, point)
}

// EnvironmentBranchLifetimeProblem refuses an extend past a branch's longest
// lifetime.
func EnvironmentBranchLifetimeProblem(branch state.ProjectEnvironmentBranch, hasCopies bool, expiresAt time.Time) *api.Problem {
	return environmentBranchLifetimeProblem(branch, hasCopies, expiresAt)
}
