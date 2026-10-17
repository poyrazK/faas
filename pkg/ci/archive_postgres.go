package ci

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func archiveRow(row sqlc.CiExecutionArchive) (ExecutionArchive, error) {
	var out ExecutionArchive
	if json.Unmarshal(row.Receipt, &out) != nil || out.ExecutionID != uuidString(row.ExecutionID) || !out.ArchivedAt.Equal(row.ArchivedAt.Time) || !out.Rerun.valid(out.ExecutionID) {
		return out, errors.New("CI archive receipt identity mismatch")
	}
	return out, nil
}

func (s *PostgresStore) ExecutionArchive(ctx context.Context, account, id string) (ExecutionArchive, error) {
	row, err := s.q.GetCIExecutionArchive(ctx, s.pool, sqlc.GetCIExecutionArchiveParams{AccountID: dbUUID(account), ExecutionID: dbUUID(id)})
	if err != nil {
		return ExecutionArchive{}, storeError(err)
	}
	return archiveRow(row)
}

func (s *PostgresStore) ExecutionArchives(ctx context.Context, account string) ([]ExecutionArchive, error) {
	rows, err := s.q.ListCIExecutionArchives(ctx, s.pool, sqlc.ListCIExecutionArchivesParams{AccountID: dbUUID(account), MaxRows: api.CIMaxExecutionArchivesPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]ExecutionArchive, 0, len(rows))
	for _, row := range rows {
		item, err := archiveRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// Follow the storage reservation lock order. No network call or native action
// occurs here; these locks fence source writes, runtime grants and late starts.
func (s *PostgresStore) ArchiveExecution(ctx context.Context, account, id string) (ExecutionArchive, bool, error) {
	tx, _, err := s.storageTx(ctx)
	if err != nil {
		return ExecutionArchive{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.q.LockCIAccount(ctx, tx, dbUUID(account)); err != nil {
		return ExecutionArchive{}, false, storeError(err)
	}
	existing, err := s.q.GetCIExecutionArchive(ctx, tx, sqlc.GetCIExecutionArchiveParams{AccountID: dbUUID(account), ExecutionID: dbUUID(id)})
	if err == nil {
		receipt, err := archiveRow(existing)
		return receipt, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExecutionArchive{}, false, storeError(err)
	}
	stored, err := s.q.LockCIExecution(ctx, tx, sqlc.LockCIExecutionParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return ExecutionArchive{}, false, storeError(err)
	}
	if stored.Mode != ExecutionJobs {
		return ExecutionArchive{}, false, ErrNotFound
	}
	row, err := executionRow(stored)
	if err != nil {
		return ExecutionArchive{}, false, err
	}
	if !terminal(row.Execution.Status) || row.Execution.FinishedAt == nil || stored.LeaseUntil.Valid {
		return ExecutionArchive{}, false, ErrArchiveUnsafe
	}
	count, err := s.q.CountCIExecutionArchives(ctx, tx, dbUUID(account))
	if err != nil {
		return ExecutionArchive{}, false, storeError(err)
	}
	if count >= api.CIMaxExecutionArchivesPerAccount {
		return ExecutionArchive{}, false, &LimitError{Resource: "execution_archives", Limit: api.CIMaxExecutionArchivesPerAccount, Observed: int(count) + 1}
	}
	check, err := s.archiveCheck(ctx, tx, row)
	if err != nil {
		return ExecutionArchive{}, false, err
	}
	safe, err := s.q.CIArchiveResourcesSafe(ctx, tx, sqlc.CIArchiveResourcesSafeParams{AccountID: dbUUID(account), ExecutionID: dbUUID(id)})
	if err != nil {
		return ExecutionArchive{}, false, storeError(err)
	}
	if !safe.Valid || !safe.Bool {
		return ExecutionArchive{}, false, ErrArchiveUnsafe
	}
	now, err := s.q.CIExecutionClock(ctx, tx)
	if err != nil {
		return ExecutionArchive{}, false, storeError(err)
	}
	e := row.Execution
	receipt := ExecutionArchive{ExecutionID: e.ID, RevisionID: e.RevisionID, Pipeline: e.Pipeline, CommitSHA: e.CommitSHA, Status: e.Status, DefinitionDigest: e.Plan.DefinitionDigest,
		ProfileID: e.ProfileID, Automation: e.Automation, Rerun: e.Rerun, CreatedAt: e.CreatedAt, FinishedAt: *e.FinishedAt, ArchivedAt: now.Time, Check: check, Jobs: make([]ArchivedJob, 0, len(e.Jobs))}
	reports, err := ExecutionReports(e)
	if err != nil {
		return ExecutionArchive{}, false, err
	}
	for _, job := range e.Jobs {
		if !terminal(job.Status) {
			return ExecutionArchive{}, false, ErrArchiveUnsafe
		}
		item := ArchivedJob{Name: job.Name, Status: job.Status}
		for _, report := range reports.Jobs {
			if report.Job == job.Name {
				totals := report.Totals
				item.TestReports = &totals
			}
		}
		if job.Attempt != nil {
			item.AttemptID = job.Attempt.ID
		}
		receipt.Jobs = append(receipt.Jobs, item)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return ExecutionArchive{}, false, err
	}
	if len(raw) > api.CIExecutionArchiveMaxBytes {
		return ExecutionArchive{}, false, &LimitError{Resource: "execution_archive_bytes", Limit: api.CIExecutionArchiveMaxBytes, Observed: len(raw)}
	}
	if err := s.compactArchive(ctx, tx, account, id, raw, receipt); err != nil {
		return ExecutionArchive{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionArchive{}, false, storeError(err)
	}
	return receipt, false, nil
}

func (s *PostgresStore) archiveCheck(ctx context.Context, tx pgx.Tx, row executionRecord) (*ArchivedCheck, error) {
	e := row.Execution
	c, err := s.q.TryLockCICheckDelivery(ctx, tx, sqlc.TryLockCICheckDeliveryParams{AccountID: dbUUID(e.AccountID), ExecutionID: dbUUID(e.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		exists, err := s.q.CIArchiveHasCheck(ctx, tx, sqlc.CIArchiveHasCheckParams{AccountID: dbUUID(e.AccountID), ExecutionID: dbUUID(e.ID)})
		if err != nil {
			return nil, storeError(err)
		}
		if exists {
			return nil, ErrArchiveUnsafe
		}
		return nil, nil
	}
	if err != nil {
		return nil, storeError(err)
	}
	if c.Status != "ignored" && (c.Status != "sent" || c.DeliveredVersion != row.Version) {
		return nil, ErrArchiveUnsafe
	}
	retries, err := s.q.CIArchiveRetries(ctx, tx, sqlc.CIArchiveRetriesParams{AccountID: dbUUID(e.AccountID), ExecutionID: dbUUID(e.ID), MaxRows: api.CICheckRetryMaxGenerations})
	if err != nil {
		return nil, storeError(err)
	}
	out := &ArchivedCheck{Status: c.Status, CheckID: c.CheckID, DeliveredVersion: c.DeliveredVersion, LastError: c.LastError, RetryGeneration: c.RetryGeneration, Retries: make([]CheckRetry, 0, len(retries))}
	for _, retry := range retries {
		if !retry.AppliedAt.Valid {
			return nil, ErrArchiveUnsafe
		}
		out.Retries = append(out.Retries, checkRetryRow(retry))
	}
	return out, nil
}

func (s *PostgresStore) compactArchive(ctx context.Context, tx pgx.Tx, account, id string, raw []byte, receipt ExecutionArchive) error {
	if err := s.q.InsertCIExecutionArchive(ctx, tx, sqlc.InsertCIExecutionArchiveParams{AccountID: dbUUID(account), ExecutionID: dbUUID(id), Receipt: raw, ArchivedAt: timestamp(receipt.ArchivedAt)}); err != nil {
		return storeError(err)
	}
	if err := s.q.DeleteCIArchiveRuntime(ctx, tx, sqlc.DeleteCIArchiveRuntimeParams{AccountID: dbUUID(account), ExecutionID: dbUUID(id)}); err != nil {
		return storeError(err)
	}
	if err := s.q.CompactCIArchiveAttempts(ctx, tx, sqlc.CompactCIArchiveAttemptsParams{AccountID: dbUUID(account), ExecutionID: dbUUID(id)}); err != nil {
		return storeError(err)
	}
	n, err := s.q.CompactCIExecution(ctx, tx, sqlc.CompactCIExecutionParams{AccountID: dbUUID(account), ID: dbUUID(id), ArchivedAt: timestamp(receipt.ArchivedAt)})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrArchiveUnsafe
	}
	return nil
}

var _ ArchiveStore = (*PostgresStore)(nil)
