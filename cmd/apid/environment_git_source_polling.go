package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentGitSourceReader struct{ server *server }

var _ environmentgitops.SourceReader = (*environmentGitSourceReader)(nil)

func (reader *environmentGitSourceReader) ReadEnvironmentGitSource(ctx context.Context, source state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
	s := reader.server
	account, err := s.store.AccountByID(ctx, source.AccountID)
	if err != nil {
		return state.EnvironmentGitSourcePollResult{}, err
	}
	if _, err := s.verifyEnvironmentGitRepository(ctx, account.ID, source.Spec.InstallationID, source.Spec.Repository, source.Spec.RepositoryID); err != nil {
		return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_repository_unavailable"}, nil
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return state.EnvironmentGitSourcePollResult{}, state.ErrInvalidArgument
	}
	maxBytes := int64(limits.SourceTarballMaxMB) << 20
	stream, err := s.githubd.StreamSourceRef(ctx, account.ID, source.Spec.InstallationID, source.Spec.Repository, source.Spec.Ref, maxBytes)
	if err != nil || stream == nil || stream.Body == nil {
		return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"}, nil
	}
	desired, parseErr := environmentgitops.ReadGitDefinition(stream.Body, source.Spec.ManifestPath, maxBytes)
	closeErr := stream.Body.Close()
	if closeErr != nil || stream.Stats == nil || stream.Stats.Err != nil || stream.Stats.Truncated || !isCanonicalCommitSHA(stream.Stats.ResolvedCommitSHA) {
		return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"}, nil
	}
	if parseErr != nil {
		return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_definition_invalid"}, nil
	}
	if err := s.verifyEnvironmentGitDefinitionScope(ctx, source, desired); err != nil {
		return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_scope_mismatch"}, nil
	}
	return state.EnvironmentGitSourcePollResult{CommitSHA: stream.Stats.ResolvedCommitSHA, Digest: desired.Digest}, nil
}

func (s *server) verifyEnvironmentGitDefinitionScope(ctx context.Context, source state.EnvironmentGitSource, desired environmentsync.DesiredState) error {
	project, err := s.store.ProjectByID(ctx, source.ProjectID)
	if err != nil {
		return err
	}
	if project.AccountID != source.AccountID || desired.Definition.Project != project.Slug || desired.Definition.Environment != source.EnvironmentSlug {
		return state.ErrInvalidArgument
	}
	return nil
}

// Discovery is safe to start independently from the full graph executor. It
// writes source availability/candidates only, and never grants approval.
func (s *server) startEnvironmentGitSourcePolling(ctx context.Context, getenv func(string) string) {
	enabled := !strings.EqualFold(getenv("FAAS_ENVIRONMENT_GIT_SOURCE_POLLING_ENABLED"), "false")
	s.environmentGitSourcePollingEnabled.Store(enabled)
	store, ok := s.store.(state.EnvironmentGitSourcePollStore)
	if !ok || !enabled {
		return
	}
	poller := &environmentgitops.SourcePoller{Store: store, Reader: &environmentGitSourceReader{server: s}, Log: s.log,
		LeaseDuration: api.EnvironmentGitSourcePollLeaseDuration, ReadTimeout: api.EnvironmentGitSourcePollReadTimeout,
		CheckInterval: api.EnvironmentGitSourcePollCheckInterval, RetryInterval: api.EnvironmentGitSourcePollRetryInterval,
	}
	go func() { _ = poller.Run(ctx, api.EnvironmentGitSourcePollIdleInterval) }()
}
