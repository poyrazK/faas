package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func inventoryScanFromSQL(r sqlc.ObjectStorageInventoryScan) ObjectInventoryScan {
	return ObjectInventoryScan{StartedAt: r.StartedAt.Time, ProgressAt: r.ProgressAt.Time, After: r.AfterKey, Bytes: r.ListedBytes, Keys: r.ListedKeys, Pages: int(r.Pages), Complete: r.Complete}
}

// ClaimObjectInventoryScan locks the usage row before the scan row, like the
// checkpoint and the publication (ADR-20261030100835).
func (s *pgCore) ClaimObjectInventoryScan(ctx context.Context, bucket, token string) (ObjectInventoryScan, error) {
	if token == "" {
		return ObjectInventoryScan{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectInventoryScan{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	n, err := q.ObjectInventoryScanClaim(ctx, tx, sqlc.ObjectInventoryScanClaimParams{ID: mustPgUUID(bucket), Token: token})
	if err != nil {
		return ObjectInventoryScan{}, err
	}
	if n != 1 {
		return ObjectInventoryScan{}, ErrConflict
	}
	var scan ObjectInventoryScan
	var observedAt time.Time
	row, err := q.ObjectInventoryScanGet(ctx, tx, mustPgUUID(bucket))
	switch {
	case err == nil:
		scan, observedAt = inventoryScanFromSQL(sqlc.ObjectStorageInventoryScan{StartedAt: row.StartedAt, ProgressAt: row.ProgressAt, AfterKey: row.AfterKey, ListedBytes: row.ListedBytes, ListedKeys: row.ListedKeys, Pages: row.Pages, Complete: row.Complete}), row.ObservedAt.Time
	case !errors.Is(err, pgx.ErrNoRows):
		return ObjectInventoryScan{}, err
	}
	resumable := inventoryScanResumable(scan, observedAt, time.Now())
	if !resumable {
		started, err := q.ObjectInventoryScanStart(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return ObjectInventoryScan{}, err
		}
		scan = inventoryScanFromSQL(started)
	}
	if !resumable || scan.Complete {
		if n, err = q.ObjectInventoryScanAttempt(ctx, tx, sqlc.ObjectInventoryScanAttemptParams{BucketID: mustPgUUID(bucket), Token: token}); err != nil {
			return ObjectInventoryScan{}, err
		}
		if n != 1 {
			return ObjectInventoryScan{}, ErrConflict
		}
	}
	return scan, tx.Commit(ctx)
}

func (s *pgCore) CheckpointObjectInventoryScan(ctx context.Context, bucket, token string, scan ObjectInventoryScan) error {
	if token == "" || !validInventoryScanProgress(scan) {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	n, err := q.ObjectInventoryRelease(ctx, tx, sqlc.ObjectInventoryReleaseParams{BucketID: mustPgUUID(bucket), Token: token})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	n, err = q.ObjectInventoryScanCheckpoint(ctx, tx, sqlc.ObjectInventoryScanCheckpointParams{
		BucketID: mustPgUUID(bucket), StartedAt: objectUsageTime(scan.StartedAt), AfterKey: scan.After,
		ListedBytes: scan.Bytes, ListedKeys: scan.Keys, Pages: int32(scan.Pages), Complete: scan.Complete,
	})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *pgCore) FinishObjectInventoryScan(ctx context.Context, bucket, token string, scan ObjectInventoryScan) error {
	if token == "" || !scan.Complete || !validInventoryScanProgress(scan) {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	account, err := q.ObjectUsageBucketAccount(ctx, tx, mustPgUUID(bucket))
	if err != nil {
		return mapErr(err)
	}
	// Serialize inventory publication with account-wide quota decisions.
	if _, err = lockObjectUsageScope(ctx, tx, account, mustPgUUID(bucket)); err != nil {
		return mapErr(err)
	}
	n, err := q.ObjectInventoryScanFinish(ctx, tx, sqlc.ObjectInventoryScanFinishParams{
		BucketID: mustPgUUID(bucket), Token: token, StartedAt: objectUsageTime(scan.StartedAt), ListedBytes: scan.Bytes, ListedKeys: scan.Keys,
	})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	if err = q.ObjectInventoryScanDelete(ctx, tx, mustPgUUID(bucket)); err != nil {
		return err
	}
	if err = q.ObjectInventorySample(ctx, tx, sqlc.ObjectInventorySampleParams{BucketID: mustPgUUID(bucket), Token: token}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
