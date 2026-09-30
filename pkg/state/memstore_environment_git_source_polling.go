package state

import (
	"context"
	"time"
)

type environmentGitSourcePoll struct {
	NextPollAt time.Time
	LeaseToken string
	LeaseUntil time.Time
}

var _ EnvironmentGitSourcePollStore = (*MemStore)(nil)

func (m *MemStore) ClaimEnvironmentGitSourcePoll(_ context.Context, token string, now time.Time, duration time.Duration) (EnvironmentGitSourcePollLease, error) {
	if err := validateEnvironmentSourcePollLease(token, now, duration); err != nil {
		return EnvironmentGitSourcePollLease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidate *environmentGitOpsMemory
	for _, memory := range m.environmentGitOps {
		poll := memory.poll
		if memory.source.Suspended || poll.NextPollAt.After(now) || poll.LeaseUntil.After(now) {
			continue
		}
		if candidate == nil || poll.NextPollAt.Before(candidate.poll.NextPollAt) ||
			poll.NextPollAt.Equal(candidate.poll.NextPollAt) && memory.source.ID < candidate.source.ID {
			candidate = memory
		}
	}
	if candidate == nil {
		return EnvironmentGitSourcePollLease{}, ErrNotFound
	}
	candidate.poll.LeaseToken, candidate.poll.LeaseUntil = token, now.Add(duration)
	return EnvironmentGitSourcePollLease{Source: cloneEnvironmentGitSource(candidate.source), LeaseToken: token, LeaseUntil: candidate.poll.LeaseUntil}, nil
}

func (m *MemStore) FinishEnvironmentGitSourcePoll(_ context.Context, lease EnvironmentGitSourcePollLease, result EnvironmentGitSourcePollResult, now, next time.Time) error {
	if err := validateEnvironmentSourcePollResult(result); err != nil || now.IsZero() || !next.After(now) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory := m.environmentGitOps[lease.Source.ID]
	if memory == nil || memory.source.AccountID != lease.Source.AccountID || memory.source.Generation != lease.Source.Generation ||
		memory.source.Suspended || memory.poll.LeaseToken != lease.LeaseToken || !memory.poll.LeaseUntil.After(now) {
		return ErrConflict
	}
	checked := now.UTC()
	memory.source.SourceCheckedAt, memory.source.SourceErrorCode = &checked, result.ErrorCode
	if result.ErrorCode == "" {
		verified := checked
		memory.source.SourceCommitSHA, memory.source.SourceDefinitionDigest, memory.source.SourceVerifiedAt = result.CommitSHA, result.Digest, &verified
	}
	memory.poll = environmentGitSourcePoll{NextPollAt: next.UTC()}
	return nil
}
