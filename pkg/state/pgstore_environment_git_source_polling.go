package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitSourcePollStore = (*PgStore)(nil)

func (s *PgStore) ClaimEnvironmentGitSourcePoll(ctx context.Context, token string, now time.Time, duration time.Duration) (EnvironmentGitSourcePollLease, error) {
	if err := validateEnvironmentSourcePollLease(token, now, duration); err != nil {
		return EnvironmentGitSourcePollLease{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentGitSourcePollLease{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	poll, err := q.ClaimEnvironmentGitSourcePoll(ctx, tx, sqlc.ClaimEnvironmentGitSourcePollParams{
		LeaseToken: mustPgUUID(token), NowAt: gitOpsTime(now), LeaseUntil: gitOpsTime(now.Add(duration)),
	})
	if err != nil {
		return EnvironmentGitSourcePollLease{}, mapErr(err)
	}
	row, err := q.GetEnvironmentGitSourceByID(ctx, tx, poll.SourceID)
	if err != nil {
		return EnvironmentGitSourcePollLease{}, mapErr(err)
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, poll.SourceID)
	if err != nil {
		return EnvironmentGitSourcePollLease{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentGitSourcePollLease{}, mapErr(err)
	}
	return EnvironmentGitSourcePollLease{Source: environmentGitSourceFromSQL(row, scope.EnvironmentSlug), LeaseToken: token, LeaseUntil: poll.LeaseUntil.Time}, nil
}

func (s *PgStore) FinishEnvironmentGitSourcePoll(ctx context.Context, lease EnvironmentGitSourcePollLease, result EnvironmentGitSourcePollResult, now, next time.Time) error {
	if err := validateEnvironmentSourcePollResult(result); err != nil || now.IsZero() || !next.After(now) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	source, err := q.LockEnvironmentGitSource(ctx, tx, sqlc.LockEnvironmentGitSourceParams{
		AccountID: mustPgUUID(lease.Source.AccountID), SourceID: mustPgUUID(lease.Source.ID),
	})
	if err != nil {
		return mapErr(err)
	}
	if source.Generation != lease.Source.Generation || source.Suspended {
		return ErrConflict
	}
	if _, err := q.LockEnvironmentGitSourcePoll(ctx, tx, sqlc.LockEnvironmentGitSourcePollParams{SourceID: source.ID, LeaseToken: mustPgUUID(lease.LeaseToken), NowAt: gitOpsTime(now)}); err != nil {
		return ErrConflict
	}
	if err := validateEnvironmentPollApproval(environmentGitSourceFromSQL(source, lease.Source.EnvironmentSlug), result); err != nil {
		return err
	}
	if result.Approval != nil {
		if err := approveEnvironmentGitPoll(ctx, tx, source, lease, result); err != nil {
			return err
		}
	}
	count, err := q.FinishEnvironmentGitSourcePoll(ctx, tx, sqlc.FinishEnvironmentGitSourcePollParams{
		SourceID: source.ID, LeaseToken: mustPgUUID(lease.LeaseToken), NowAt: gitOpsTime(now), NextPollAt: gitOpsTime(next),
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	if err := q.RecordEnvironmentGitSourcePoll(ctx, tx, sqlc.RecordEnvironmentGitSourcePollParams{
		SourceID: source.ID, CheckedAt: gitOpsTime(now), CommitSha: result.CommitSHA, DefinitionDigest: result.Digest, ErrorCode: result.ErrorCode,
	}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
