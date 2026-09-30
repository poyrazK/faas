package state

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Polling discovers candidates; neither a successful poll nor Git availability
// grants approval, changes ownership, or replaces the last approved revision.
type EnvironmentGitSourcePollLease struct {
	Source     EnvironmentGitSource
	LeaseToken string
	LeaseUntil time.Time
}

type EnvironmentGitSourcePollResult struct {
	CommitSHA string
	Digest    string
	ErrorCode string
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
		return nil
	}
	if result.CommitSHA != "" || result.Digest != "" {
		return ErrInvalidArgument
	}
	switch result.ErrorCode {
	case "environment_git_source_unavailable", "environment_git_definition_invalid", "environment_git_scope_mismatch", "environment_git_repository_unavailable":
		return nil
	default:
		return ErrInvalidArgument
	}
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
