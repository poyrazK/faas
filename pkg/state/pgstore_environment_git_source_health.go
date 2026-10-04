package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitSourceHealthStore = (*PgStore)(nil)

func (s *PgStore) EnvironmentGitSourceHealth(ctx context.Context, now time.Time, staleAfter time.Duration) (EnvironmentGitSourceHealth, error) {
	if err := validateEnvironmentGitSourceHealth(now, staleAfter); err != nil {
		return EnvironmentGitSourceHealth{}, err
	}
	row, err := sqlc.New().EnvironmentGitSourceHealth(ctx, s.pool, sqlc.EnvironmentGitSourceHealthParams{
		NowAt: gitOpsTime(now), StaleBefore: gitOpsTime(now.Add(-staleAfter)),
	})
	if err != nil {
		return EnvironmentGitSourceHealth{}, mapErr(err)
	}
	return EnvironmentGitSourceHealth{
		Active: row.Active, Suspended: row.Suspended, Unchecked: row.Unchecked, Unverified: row.Unverified,
		PollStale: row.PollStale, VerificationStale: row.VerificationStale, Unavailable: row.Unavailable,
		CandidatePendingApproval: row.CandidatePendingApproval, ApprovedPendingApply: row.ApprovedPendingApply,
		OldestCheckAgeSeconds: row.OldestCheckAgeSeconds, OldestVerificationAgeSeconds: row.OldestVerificationAgeSeconds,
	}, nil
}
