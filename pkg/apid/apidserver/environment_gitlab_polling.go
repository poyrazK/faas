package apidserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

//nolint:nilerr // Provider errors become durable classified poll results.
func (reader *environmentGitSourceReader) readGitLab(ctx context.Context, account state.Account, source state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error) {
	s := reader.server
	client, token, sha, err := s.resolveVerifiedGitLabCommit(ctx, account.ID, source, source.Spec.Ref)
	if err == nil && source.Spec.ApprovalPolicy == "manual" && source.SourceVerifiedAt != nil && source.SourceCommitSHA == sha && source.SourceDefinitionDigest != "" {
		return state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: source.SourceDefinitionDigest}, nil
	}
	var desired environmentsync.DesiredState
	if err == nil {
		desired, err = s.readGitLabDefinitionAtCommit(ctx, account, source, client, token, sha)
	}
	if err != nil {
		code := "environment_git_source_unavailable"
		switch {
		case errors.Is(err, errGitLabRepositoryUnavailable):
			code = "environment_git_repository_unavailable"
		case errors.Is(err, errGitLabDefinitionInvalid):
			code = "environment_git_definition_invalid"
		case errors.Is(err, errGitLabScopeMismatch):
			code = "environment_git_scope_mismatch"
		}
		return state.EnvironmentGitSourcePollResult{ErrorCode: code}, nil
	}
	result := state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest}
	if source.Spec.ApprovalPolicy == "protected_branch" {
		branch := strings.TrimPrefix(source.Spec.Ref, "refs/heads/")
		evidence, err := client.ReviewedMergeEvidence(ctx, token, source.Spec.RepositoryID, source.Spec.Repository, branch, sha)
		if err != nil {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_unavailable"}, nil
		}
		if !evidence.ValidForProvider("gitlab", 0, source.Spec.RepositoryID, source.Spec.Repository, branch, sha, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
			return state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_approval_not_qualified"}, nil
		}
		reviewed, err := s.readGitLabDefinitionAtCommit(ctx, account, source, client, token, evidence.HeadSHA)
		if err != nil {
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
