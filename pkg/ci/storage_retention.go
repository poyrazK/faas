package ci

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) storageCandidates(ctx context.Context) ([]string, error) {
	now, err := s.executionNow(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	keys, err := s.q.ListCIStorageCandidates(ctx, s.pool, sqlc.ListCIStorageCandidatesParams{Now: timestamp(now), OrphanBefore: timestamp(now.Add(-time.Duration(api.CIStorageOrphanHours) * time.Hour)), Batch: api.CIStorageCleanupBatch})
	return keys, storeError(err)
}

func (s *PostgresStore) claimStorageRetirement(ctx context.Context, key string) (storageClaim, error) {
	tx, now, err := s.storageTx(ctx)
	if err != nil {
		return storageClaim{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.LockCIManagedObject(ctx, tx, key)
	if err != nil {
		return storageClaim{}, storeError(err)
	}
	if row.State == "deleted" || row.State == "ready" && now.Before(row.ExpiresAt.Time) || row.State == "pending" && now.Before(row.CreatedAt.Time.Add(time.Duration(api.CIStorageOrphanHours)*time.Hour)) {
		return storageClaim{}, ErrConflict
	}
	safe, err := s.q.CIStorageRetirementSafe(ctx, tx, sqlc.CIStorageRetirementSafeParams{ID: row.ExecutionID, AccountID: row.AccountID, AttemptID: row.AttemptID, JobID: row.JobID, Now: timestamp(now)})
	if err != nil || !safe.Valid || !safe.Bool {
		return storageClaim{}, ErrConflict
	}
	claim, err := s.beginStorageOperation(ctx, tx, row, now, "deleting")
	if err != nil {
		return storageClaim{}, err
	}
	return claim, storeError(tx.Commit(ctx))
}

func (s *PostgresStore) commitStorageRetirement(ctx context.Context, claim storageClaim) error {
	tx, now, err := s.storageTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	count, err := s.q.CommitCIStorageRetirement(ctx, tx, sqlc.CommitCIStorageRetirementParams{ObjectKey: claim.Row.ObjectKey, OpToken: dbUUID(claim.Token), DeletedAt: timestamp(now)})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	if err := s.q.RetireCIStorageRuntime(ctx, tx, sqlc.RetireCIStorageRuntimeParams{AttemptID: claim.Row.AttemptID, Now: timestamp(now)}); err != nil {
		return storeError(err)
	}
	return storeError(tx.Commit(ctx))
}
