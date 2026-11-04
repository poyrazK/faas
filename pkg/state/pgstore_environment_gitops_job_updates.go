package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsJobUpdateStore = (*PgStore)(nil)

func (s *PgStore) PrepareEnvironmentGitOpsJobUpdates(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err := q.LockEnvironmentGitOpsIntentApps(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
		return nil, mapErr(err)
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return nil, err
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, lease.Source, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(lease.Source, lease.Revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if lease.Source.Spec.Mode != "enforce" || !plan.CanApply() || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, err
	}
	steps := []EnvironmentGitOpsStep{}
	for _, target := range gitOpsJobUpdates(snapshot, plan, observed.State.ResourceIDs) {
		status, err := q.LockEnvironmentGitOpsJobUpdate(ctx, tx, sqlc.LockEnvironmentGitOpsJobUpdateParams{
			AccountID: mustPgUUID(lease.Source.AccountID), AppID: mustPgUUID(target.AppID),
			EnvironmentID: mustPgUUID(lease.Source.EnvironmentID), JobID: mustPgUUID(target.JobID),
		})
		if err != nil {
			return nil, mapErr(err)
		}
		if status == "deleted" {
			continue
		}
		active, err := q.EnvironmentGitOpsJobHasActiveTasks(ctx, tx, mustPgUUID(target.JobID))
		if err != nil {
			return nil, mapErr(err)
		}
		steps = append(steps, gitOpsJobUpdateStep(target, status, active)...)
		if status == "active" {
			if err := q.PauseEnvironmentGitOpsJobUpdate(ctx, tx, sqlc.PauseEnvironmentGitOpsJobUpdateParams{
				JobID: mustPgUUID(target.JobID), UpdatedAt: gitOpsTime(time.Now()),
			}); err != nil {
				return nil, mapErr(err)
			}
		}
	}
	return steps, mapErr(tx.Commit(ctx))
}
