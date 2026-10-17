package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func (stubGithubdClient) GetCIPullRequest(context.Context, string, int64, string, int64) (githubdgrpc.CIPullRequest, error) {
	return githubdgrpc.CIPullRequest{}, ci.ErrGitHubUnavailable
}

func (l *liveClient) GetCIPullRequest(ctx context.Context, account string, installation int64, repository string, number int64) (githubdgrpc.CIPullRequest, error) {
	return l.c.GetCIPullRequest(ctx, account, installation, repository, number)
}

func (s *server) resolveCIAutomationPullRequest(ctx context.Context, binding ci.GitHubBinding, number int) (ci.PullRequestState, error) {
	provider, ok := s.githubd.(githubdgrpc.CIPullRequestService)
	if !ok {
		return ci.PullRequestState{}, ci.ErrGitHubUnavailable
	}
	pr, err := provider.GetCIPullRequest(ctx, binding.AccountID, binding.InstallationID, binding.Repository, int64(number))
	if err != nil {
		return ci.PullRequestState{}, ci.ErrGitHubUnavailable
	}
	return pr, nil
}
