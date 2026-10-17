package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func (stubGithubdClient) GetCIChangedFiles(context.Context, string, int64, string, string, string) (githubdgrpc.CIChangedFiles, error) {
	return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
}
func (l *liveClient) GetCIChangedFiles(ctx context.Context, account string, installation int64, repository, base, head string) (githubdgrpc.CIChangedFiles, error) {
	return l.c.GetCIChangedFiles(ctx, account, installation, repository, base, head)
}
func (s *server) resolveCIAutomationChanges(ctx context.Context, binding ci.GitHubBinding, base, head string) (ci.ChangedFiles, error) {
	provider, ok := s.githubd.(githubdgrpc.CIChangedFilesService)
	if !ok {
		return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
	}
	changes, err := provider.GetCIChangedFiles(ctx, binding.AccountID, binding.InstallationID, binding.Repository, base, head)
	if err != nil {
		return ci.ChangedFiles{}, ci.ErrGitHubUnavailable
	}
	return changes, nil
}
