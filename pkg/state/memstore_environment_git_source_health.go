package state

import (
	"context"
	"math"
	"time"
)

var _ EnvironmentGitSourceHealthStore = (*MemStore)(nil)

func (m *MemStore) EnvironmentGitSourceHealth(ctx context.Context, now time.Time, staleAfter time.Duration) (EnvironmentGitSourceHealth, error) {
	if err := validateEnvironmentGitSourceHealth(now, staleAfter); err != nil {
		return EnvironmentGitSourceHealth{}, err
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentGitSourceHealth{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var health EnvironmentGitSourceHealth
	cutoff := now.Add(-staleAfter)
	for _, memory := range m.environmentGitOps {
		source := memory.source
		if source.Detached {
			continue
		}
		if source.Suspended {
			health.Suspended++
			continue
		}
		health.Active++
		checked, verified := source.CreatedAt, source.CreatedAt
		if source.SourceCheckedAt == nil {
			health.Unchecked++
		} else {
			checked = *source.SourceCheckedAt
		}
		if source.SourceVerifiedAt == nil {
			health.Unverified++
		} else {
			verified = *source.SourceVerifiedAt
		}
		if !checked.After(cutoff) {
			health.PollStale++
		}
		if !verified.After(cutoff) {
			health.VerificationStale++
		}
		health.OldestCheckAgeSeconds = math.Max(health.OldestCheckAgeSeconds, now.Sub(checked).Seconds())
		health.OldestVerificationAgeSeconds = math.Max(health.OldestVerificationAgeSeconds, now.Sub(verified).Seconds())
		if source.SourceErrorCode != "" {
			health.Unavailable++
		}
		approved := memory.revisions[source.ApprovedRevisionID]
		if source.SourceCommitSHA != "" && (source.SourceCommitSHA != approved.CommitSHA || source.SourceDefinitionDigest != approved.Digest) {
			health.CandidatePendingApproval++
		}
		if source.ApprovedRevisionID != "" && source.ApprovedRevisionID != source.AppliedRevisionID {
			health.ApprovedPendingApply++
		}
	}
	return health, nil
}
