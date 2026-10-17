package ci

import (
	"context"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) executionRerunReplay(ctx context.Context, account, parent, key string) (Execution, bool, error) {
	return s.rerunReplay(ctx, s.pool, account, parent, key)
}

func (s *PostgresStore) rerunReplay(ctx context.Context, db sqlc.DBTX, account, parent, key string) (Execution, bool, error) {
	stored, err := s.q.GetCIExecutionByKey(ctx, db, sqlc.GetCIExecutionByKeyParams{AccountID: dbUUID(account), IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, nil
	}
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	if !stored.RerunParentID.Valid || uuidString(stored.RerunParentID) != parent {
		return Execution{}, false, ErrConflict
	}
	row, err := executionRow(stored)
	return row.Execution, err == nil, err
}

// Account -> parent execution follows the normal admission lock order. Native
// stop/output/transfer receipts are read in the same transaction as insertion.
func (s *PostgresStore) jobsRerunParent(ctx context.Context, tx pgx.Tx, account string, req JobsSubmitRequest, binding GitHubBinding) error {
	if req.rerun == nil {
		return nil
	}
	expected := req.rerun.Parent
	stored, err := s.q.LockCIExecution(ctx, tx, sqlc.LockCIExecutionParams{AccountID: dbUUID(account), ID: dbUUID(expected.ID)})
	if err != nil {
		return storeError(err)
	}
	row, err := executionRow(stored)
	if err != nil {
		return err
	}
	parent := row.Execution
	if parent.Mode != ExecutionJobs || parent.Compute == nil || parent.ID != expected.ID || parent.AccountID != account ||
		parent.ProfileID != req.ProfileID || parent.RevisionID != req.RevisionID || parent.CommitSHA != req.CommitSHA ||
		!reflect.DeepEqual(parent.Plan, expected.Plan) || !reflect.DeepEqual(parent.Rerun, expected.Rerun) ||
		!reflect.DeepEqual(parent.Compute, expected.Compute) || !sameJobsBinding(parent.Compute.Binding, binding) {
		return ErrConflict
	}
	if !terminal(parent.Status) || parent.FinishedAt == nil || stored.LeaseUntil.Valid {
		return ErrRerunNotReady
	}
	safe, err := s.q.CIAutomationExecutionStopped(ctx, tx, sqlc.CIAutomationExecutionStoppedParams{AccountID: dbUUID(account), ExecutionID: stored.ID})
	if err != nil {
		return storeError(err)
	}
	if !safe.Valid || !safe.Bool {
		return ErrRerunNotReady
	}
	return nil
}

func sameRerunConfiguration(row, parent Execution) bool {
	return row.ProfileID == parent.ProfileID && row.RevisionID == parent.RevisionID && row.CommitSHA == parent.CommitSHA &&
		reflect.DeepEqual(row.Plan, parent.Plan) && sameJobsBinding(row.Compute.Binding, parent.Compute.Binding) &&
		reflect.DeepEqual(row.Compute.Templates, parent.Compute.Templates)
}

var _ ExecutionRerunStore = (*PostgresStore)(nil)
