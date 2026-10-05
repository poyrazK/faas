package state

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gitapproval"
)

// Manual sources discover candidates. Protected sources require fresh reviewed
// merge evidence and approve exact definitions inside the fenced poll transaction.
type EnvironmentGitSourcePollLease struct {
	Source     EnvironmentGitSource
	LeaseToken string
	LeaseUntil time.Time
}

type EnvironmentGitSourcePollResult struct {
	CommitSHA string
	Digest    string
	ErrorCode string
	Desired   *environmentsync.DesiredState
	Approval  *gitapproval.MergeEvidence
}

type EnvironmentGitSourcePollStore interface {
	ClaimEnvironmentGitSourcePoll(context.Context, string, time.Time, time.Duration) (EnvironmentGitSourcePollLease, error)
	FinishEnvironmentGitSourcePoll(context.Context, EnvironmentGitSourcePollLease, EnvironmentGitSourcePollResult, time.Time, time.Time) error
}

var environmentDefinitionDigestRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateEnvironmentSourcePollResult(result EnvironmentGitSourcePollResult) error {
	if result.ErrorCode == "" {
		if !environmentCommitRE.MatchString(result.CommitSHA) || !environmentDefinitionDigestRE.MatchString(result.Digest) {
			return ErrInvalidArgument
		}
		if (result.Desired == nil) != (result.Approval == nil) {
			return ErrInvalidArgument
		}
		if result.Desired != nil {
			desired, err := environmentsync.Compile(result.Desired.Definition)
			raw, marshalErr := json.Marshal(result.Approval)
			if err != nil || desired.Digest != result.Digest || result.Desired.Digest != result.Digest || marshalErr != nil || len(raw) > api.EnvironmentGitApprovalEvidenceMaxBytes {
				return ErrInvalidArgument
			}
		}
		return nil
	}
	if result.CommitSHA != "" || result.Digest != "" || result.Desired != nil || result.Approval != nil {
		return ErrInvalidArgument
	}
	switch result.ErrorCode {
	case "environment_git_source_unavailable", "environment_git_definition_invalid", "environment_git_scope_mismatch", "environment_git_repository_unavailable", "environment_git_approval_unavailable", "environment_git_approval_not_qualified":
		return nil
	default:
		return ErrInvalidArgument
	}
}

func validateEnvironmentPollApproval(source EnvironmentGitSource, result EnvironmentGitSourcePollResult) error {
	if result.ErrorCode != "" {
		return nil
	}
	if source.Spec.ApprovalPolicy == "manual" {
		if result.Approval != nil {
			return ErrInvalidArgument
		}
		return nil
	}
	if source.Spec.ApprovalPolicy != "protected_branch" || result.Approval == nil || result.Desired == nil || result.Approval.ReviewedDefinitionDigest != result.Digest ||
		!result.Approval.ValidFor(source.Spec.InstallationID, source.Spec.RepositoryID, source.Spec.Repository,
			strings.TrimPrefix(source.Spec.Ref, "refs/heads/"), result.CommitSHA, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) {
		return ErrInvalidArgument
	}
	return nil
}

func validateEnvironmentSourcePollLease(token string, now time.Time, duration time.Duration) error {
	if _, err := uuid.Parse(token); err != nil || now.IsZero() || duration <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func cloneEnvironmentGitSource(source EnvironmentGitSource) EnvironmentGitSource {
	if source.SourceCheckedAt != nil {
		at := *source.SourceCheckedAt
		source.SourceCheckedAt = &at
	}
	if source.SourceVerifiedAt != nil {
		at := *source.SourceVerifiedAt
		source.SourceVerifiedAt = &at
	}
	return source
}
