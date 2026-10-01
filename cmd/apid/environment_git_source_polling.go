package main

import (
	"context"
	"strings"
	"time"

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
	result := state.EnvironmentGitSourcePollResult{CommitSHA: stream.Stats.ResolvedCommitSHA, Digest: desired.Digest}
	if source.Spec.ApprovalPolicy == "protected_branch" {
		client, ok := s.githubd.(githubdReviewedMergeClient)
		if !ok {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_unavailable"}, nil
		}
		evidence, err := client.GetReviewedMergeEvidence(ctx, source.AccountID, source.Spec.InstallationID, source.Spec.RepositoryID,
			source.Spec.Repository, strings.TrimPrefix(source.Spec.Ref, "refs/heads/"), result.CommitSHA)
		if err != nil {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_unavailable"}, nil
		}
		if !evidence.ValidFor(source.Spec.InstallationID, source.Spec.RepositoryID, source.Spec.Repository,
			strings.TrimPrefix(source.Spec.Ref, "refs/heads/"), result.CommitSHA, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_not_qualified"}, nil
		}
		reviewed, problem := s.readEnvironmentGitRevision(ctx, account, source, evidence.HeadSHA)
		if problem != nil {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_unavailable"}, nil
		}
		if reviewed.Digest != desired.Digest {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_not_qualified"}, nil
		}
		evidence.ReviewedDefinitionDigest = reviewed.Digest
		result.Desired, result.Approval = &desired, &evidence
	}
	return result, nil
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
// verifies candidates and atomically approves qualified protected-branch merges.
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
