package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func executionJSON(row Execution) ([]byte, error) {
	data, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode CI execution: %w", err)
	}
	if len(data) > api.CIExecutionSnapshotMaxBytes {
		return nil, &LimitError{Resource: "execution_snapshot_bytes", Limit: api.CIExecutionSnapshotMaxBytes, Observed: len(data)}
	}
	return data, nil
}

func executionRow(row sqlc.CiExecution) (executionRecord, error) {
	if row.ArchivedAt.Valid {
		return executionRecord{}, ErrExecutionArchived
	}
	out := executionRecord{Version: row.Version, Fence: row.Fence}
	if row.LeaseUntil.Valid {
		out.Until = row.LeaseUntil.Time
	}
	if err := json.Unmarshal(row.Snapshot, &out.Execution); err != nil {
		return executionRecord{}, fmt.Errorf("decode CI execution: %w", err)
	}
	e := out.Execution
	if !e.Rerun.valid(e.ID) || (e.Rerun != nil) != row.RerunParentID.Valid || (e.Rerun != nil) != row.RerunRootID.Valid ||
		e.Rerun != nil && (e.Rerun.ParentExecutionID != uuidString(row.RerunParentID) || e.Rerun.RootExecutionID != uuidString(row.RerunRootID) || e.Mode != ExecutionJobs) {
		return executionRecord{}, fmt.Errorf("CI execution rerun identity mismatch")
	}
	if (e.Mode == ExecutionJobs) != (e.Compute != nil) {
		return executionRecord{}, fmt.Errorf("CI execution compute provenance mismatch")
	}
	if e.ID != uuidString(row.ID) || e.AccountID != uuidString(row.AccountID) || e.RevisionID != uuidString(row.RevisionID) ||
		e.CommitSHA != row.CommitSha || e.Status != row.Status || e.Mode != row.Mode {
		return executionRecord{}, fmt.Errorf("CI execution snapshot identity mismatch")
	}
	return out, nil
}

func (s *PostgresStore) executionNow(ctx context.Context, db sqlc.DBTX) (time.Time, error) {
	if s.executionClock != nil {
		return s.executionClock().UTC(), nil
	}
	now, err := s.q.CIExecutionClock(ctx, db)
	return now.Time, storeError(err)
}

func (s *PostgresStore) SubmitQualification(ctx context.Context, account, key string, req SubmitRequest) (Execution, bool, error) {
	if err := validateSubmission(key, req); err != nil {
		return Execution{}, false, err
	}
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return Execution{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	existing, err := s.q.GetCIExecutionByKey(ctx, tx, sqlc.GetCIExecutionByKeyParams{AccountID: dbUUID(account), IdempotencyKey: key})
	if err == nil {
		if existing.Mode != ExecutionQualification || uuidString(existing.RevisionID) != req.RevisionID || existing.CommitSha != req.CommitSHA {
			return Execution{}, false, ErrConflict
		}
		row, err := executionRow(existing)
		return row.Execution, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, storeError(err)
	}
	revision, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(account), ID: dbUUID(req.RevisionID)})
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	count, err := s.q.CountCIExecutions(ctx, tx, dbUUID(account))
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	if count >= api.CIMaxQualificationExecutionsPerAccount {
		return Execution{}, false, &LimitError{Resource: "qualification_executions", Limit: api.CIMaxQualificationExecutionsPerAccount, Observed: int(count) + 1}
	}
	decoded, err := revisionRow(revision)
	if err != nil {
		return Execution{}, false, err
	}
	return s.insertQualification(ctx, tx, key, req, decoded)
}

func (s *PostgresStore) insertQualification(ctx context.Context, tx pgx.Tx, key string, req SubmitRequest, revision Revision) (Execution, bool, error) {
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return Execution{}, false, err
	}
	row, err := newExecution(revision, req, now)
	if err != nil {
		return Execution{}, false, err
	}
	data, err := executionJSON(row)
	if err != nil {
		return Execution{}, false, err
	}
	_, err = s.q.InsertCIExecution(ctx, tx, sqlc.InsertCIExecutionParams{
		ID: dbUUID(row.ID), AccountID: dbUUID(row.AccountID), RevisionID: dbUUID(row.RevisionID),
		CommitSha: row.CommitSHA, IdempotencyKey: key, Snapshot: data, CreatedAt: timestamp(now)})
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, false, storeError(err)
	}
	return row, false, nil
}

func (s *PostgresStore) Execution(ctx context.Context, account, id string) (Execution, error) {
	row, err := s.q.GetCIExecution(ctx, s.pool, sqlc.GetCIExecutionParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return Execution{}, storeError(err)
	}
	decoded, err := executionRow(row)
	return decoded.Execution, err
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func (s *PostgresStore) mutateExecution(ctx context.Context, account, id string, mutate func(*executionRecord, time.Time) error) (executionRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return executionRecord{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := s.q.LockCIExecution(ctx, tx, sqlc.LockCIExecutionParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return executionRecord{}, storeError(err)
	}
	row, err := executionRow(stored)
	if err != nil {
		return executionRecord{}, err
	}
	if err := s.mutateLockedExecution(ctx, tx, &row, mutate); err != nil {
		return executionRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return executionRecord{}, storeError(err)
	}
	return row, nil
}

func (s *PostgresStore) mutateLockedExecution(ctx context.Context, tx pgx.Tx, row *executionRecord, mutate func(*executionRecord, time.Time) error) error {
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return err
	}
	if err := mutate(row, now); err != nil {
		return err
	}
	e := row.Execution
	if e.Mode == ExecutionJobs && e.CancelRequested {
		if err := s.q.CancelCIJobsExecutionRuntime(ctx, tx, sqlc.CancelCIJobsExecutionRuntimeParams{ExecutionID: dbUUID(e.ID), CancelledAt: timestamp(now)}); err != nil {
			return storeError(err)
		}
	}
	data, err := executionJSON(e)
	if err != nil {
		return err
	}
	finished := pgtype.Timestamptz{}
	if e.FinishedAt != nil {
		finished = timestamp(*e.FinishedAt)
	}
	_, err = s.q.UpdateCIExecution(ctx, tx, sqlc.UpdateCIExecutionParams{AccountID: dbUUID(e.AccountID), ID: dbUUID(e.ID),
		Status: e.Status, Snapshot: data, Version: row.Version, Fence: row.Fence, LeaseUntil: timestamp(row.Until), FinishedAt: finished})
	return storeError(err)
}

func (s *PostgresStore) CancelExecution(ctx context.Context, account, id string) (Execution, error) {
	row, err := s.mutateExecution(ctx, account, id, func(row *executionRecord, now time.Time) error {
		cancelExecutionRecord(row, now)
		return nil
	})
	return row.Execution, err
}

func (s *PostgresStore) claimExecution(ctx context.Context, account, id string) (executionLease, error) {
	row, err := s.mutateExecution(ctx, account, id, claimExecutionRecord)
	return executionLease{executionRecord: row}, err
}

func (s *PostgresStore) checkExecutionLease(ctx context.Context, lease executionLease) error {
	stored, err := s.q.GetCIExecution(ctx, s.pool, sqlc.GetCIExecutionParams{AccountID: dbUUID(lease.Execution.AccountID), ID: dbUUID(lease.Execution.ID)})
	if err != nil {
		return storeError(err)
	}
	row, err := executionRow(stored)
	if err != nil {
		return err
	}
	now, err := s.executionNow(ctx, s.pool)
	if err != nil {
		return err
	}
	if !validLease(row, lease, now) {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) finishExecutionTick(ctx context.Context, lease executionLease, observations []attemptObservation) (Execution, error) {
	row, err := s.mutateExecution(ctx, lease.Execution.AccountID, lease.Execution.ID, func(row *executionRecord, now time.Time) error {
		return finishExecutionRecord(row, lease, observations, now)
	})
	return row.Execution, err
}

var _ ExecutionStore = (*PostgresStore)(nil)
