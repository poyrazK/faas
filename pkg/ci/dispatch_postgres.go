package ci

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) jobsAdmissionBudget(ctx context.Context, tx pgx.Tx, account string) error {
	if _, err := s.q.LockCIDispatchBudget(ctx, tx); err != nil {
		return storeError(err)
	}
	count, err := s.q.CountCIActiveExecutions(ctx, tx, dbUUID(account))
	if err != nil {
		return storeError(err)
	}
	if count.AccountCount >= api.CIActiveExecutionsPerAccount {
		return &LimitError{Resource: "active_executions", Limit: api.CIActiveExecutionsPerAccount, Observed: int(count.AccountCount) + 1}
	}
	if count.GlobalCount >= api.CIActiveExecutionsGlobal {
		return &LimitError{Resource: "global_active_executions", Limit: api.CIActiveExecutionsGlobal, Observed: int(count.GlobalCount) + 1}
	}
	return nil
}

func (s *PostgresStore) claimDispatch(ctx context.Context, cfg Config) (executionLease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return executionLease{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.q.LockCIDispatchBudget(ctx, tx); err != nil {
		return executionLease{}, storeError(err)
	}
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return executionLease{}, err
	}
	count, err := s.q.CountCIDispatchLeases(ctx, tx, timestamp(now))
	if err != nil {
		return executionLease{}, storeError(err)
	}
	if count >= api.CIDispatchWorkers {
		return executionLease{}, ErrNotFound
	}
	stored, err := s.q.NextCIDispatchExecution(ctx, tx, sqlc.NextCIDispatchExecutionParams{NowAt: timestamp(now), AccountLeases: api.CIDispatchLeasesPerAccount})
	if err != nil {
		return executionLease{}, storeError(err)
	}
	row, err := executionRow(stored)
	if err != nil {
		return executionLease{}, err
	}
	// Policy removal stops new work while retaining an owner to cancel native
	// tasks and collect their sealed output. This also handles allowlist removal.
	err = s.mutateLockedExecution(ctx, tx, &row, func(row *executionRecord, now time.Time) error {
		if !cfg.DispatchAllows(row.Execution.AccountID) || !cfg.TransportEnabled {
			cancelExecutionRecord(row, now)
		}
		return claimExecutionRecord(row, now)
	})
	if err != nil {
		return executionLease{}, err
	}
	next := now.Add(time.Duration(api.CIExecutionLeaseSeconds) * time.Second)
	if err := s.q.ScheduleCIDispatchClaim(ctx, tx, sqlc.ScheduleCIDispatchClaimParams{ID: dbUUID(row.Execution.ID), Fence: row.Fence, NextReconcileAt: timestamp(next)}); err != nil {
		return executionLease{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return executionLease{}, storeError(err)
	}
	return executionLease{executionRecord: row}, nil
}

func (s *PostgresStore) finishDispatch(ctx context.Context, lease executionLease, failed bool) error {
	return storeError(s.q.FinishCIDispatchSchedule(ctx, s.pool, sqlc.FinishCIDispatchScheduleParams{
		ID: dbUUID(lease.Execution.ID), Fence: lease.Fence, Failed: failed, MaxFailures: api.CIDispatchMaxFailures,
		MaxBackoff: api.CIDispatchMaxBackoffSeconds, PollSeconds: api.CIDispatchPollSeconds, ExpectedVersion: lease.Version + 1}))
}

func (s *PostgresStore) ListJobsExecutions(ctx context.Context, account string) ([]ExecutionSummary, error) {
	rows, err := s.q.ListCIJobsExecutions(ctx, s.pool, sqlc.ListCIJobsExecutionsParams{AccountID: dbUUID(account), MaxRows: api.CIMaxJobsExecutionsPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]ExecutionSummary, 0, len(rows))
	for _, row := range rows {
		item := ExecutionSummary{ID: uuidString(row.ID), RevisionID: uuidString(row.RevisionID), Pipeline: row.Pipeline, CommitSHA: row.CommitSha, Status: row.Status, CreatedAt: row.CreatedAt.Time}
		if row.RerunParentID.Valid {
			item.Rerun = &ExecutionRerun{ParentExecutionID: uuidString(row.RerunParentID), RootExecutionID: uuidString(row.RerunRootID)}
		}
		if row.FinishedAt.Valid {
			item.FinishedAt = &row.FinishedAt.Time
		}
		out = append(out, item)
	}
	return out, nil
}

var _ DispatchStore = (*PostgresStore)(nil)
