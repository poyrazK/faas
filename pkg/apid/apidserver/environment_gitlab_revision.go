package apidserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	errGitLabRepositoryUnavailable = errors.New("GitLab repository unavailable")
	errGitLabDefinitionInvalid     = errors.New("GitLab definition invalid")
	errGitLabScopeMismatch         = errors.New("GitLab definition scope mismatch")
)

// readVerifiedGitLabSource verifies the OAuth account's current permission,
// the immutable numeric project identity, and the commit before reading an
// archive. A mutable branch/tag ref is never passed to the archive endpoint.
func (s *server) readVerifiedGitLabSource(ctx context.Context, account state.Account, source state.EnvironmentGitSource, ref string) (environmentsync.DesiredState, string, error) {
	client, token, sha, err := s.resolveVerifiedGitLabCommit(ctx, account.ID, source, ref)
	if err != nil {
		return environmentsync.DesiredState{}, "", err
	}
	desired, err := s.readGitLabDefinitionAtCommit(ctx, account, source, client, token, sha)
	return desired, sha, err
}

func (s *server) resolveVerifiedGitLabCommit(ctx context.Context, accountID string, source state.EnvironmentGitSource, ref string) (gitlabsource.Client, string, string, error) {
	client := s.gitLabClient()
	if source.Spec.Provider != "gitlab" || source.Spec.RepositoryID <= 0 {
		return client, "", "", state.ErrInvalidArgument
	}
	token, err := s.gitLabTokenForAccount(ctx, accountID)
	if err != nil {
		return client, "", "", fmt.Errorf("GitLab connection unavailable: %w", err)
	}
	if _, err := client.VerifyRepository(ctx, token, source.Spec.Repository, source.Spec.RepositoryID); err != nil {
		return client, "", "", fmt.Errorf("%w: %w", errGitLabRepositoryUnavailable, err)
	}
	sha, err := client.ResolveCommit(ctx, token, source.Spec.RepositoryID, ref)
	if err != nil || !isCanonicalCommitSHA(sha) {
		return client, "", "", fmt.Errorf("GitLab commit unavailable")
	}
	if isCanonicalCommitSHA(ref) && sha != ref {
		return client, "", "", fmt.Errorf("GitLab commit identity changed")
	}
	return client, token, sha, nil
}

func (s *server) readGitLabDefinitionAtCommit(ctx context.Context, account state.Account, source state.EnvironmentGitSource, client gitlabsource.Client, token, sha string) (environmentsync.DesiredState, error) {
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return environmentsync.DesiredState{}, state.ErrInvalidArgument
	}
	archive, err := client.Archive(ctx, token, source.Spec.RepositoryID, sha, int64(limits.SourceTarballMaxMB)<<20)
	if err != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("GitLab archive unavailable: %w", err)
	}
	desired, parseErr := environmentgitops.ReadGitDefinition(archive, source.Spec.ManifestPath, int64(limits.SourceTarballMaxMB)<<20)
	closeErr := archive.Close()
	if closeErr != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("GitLab source stream unavailable: %w", closeErr)
	}
	if parseErr != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("%w: %w", errGitLabDefinitionInvalid, parseErr)
	}
	if err := s.verifyEnvironmentGitDefinitionScope(ctx, source, desired); err != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("%w: %w", errGitLabScopeMismatch, err)
	}
	return desired, nil
}
