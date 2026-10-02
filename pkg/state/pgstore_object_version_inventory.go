package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectVersionInventoryStore = (*PgStore)(nil)

func (s *PgStore) ObjectVersionAccountingStatus(ctx context.Context, account, bucket string) (ObjectVersionAccountingStatus, error) {
	r, err := sqlc.New().ObjectVersionAccountingStatus(ctx, s.pool, sqlc.ObjectVersionAccountingStatusParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	return ObjectVersionAccountingStatus{Scope: r.InventoryScope, VersionsObserved: r.VersionsObserved, NativeScanActive: r.NativeScanActive}, mapErr(err)
}

func (s *PgStore) StageObjectVersionInventoryPage(ctx context.Context, id, token, next string, items []ObjectVersionInventoryRecord) (ObjectCapacityReconciliation, error) {
	if !validVersionInventoryPage(next, items) {
		return ObjectCapacityReconciliation{}, ErrConflict
	}
	tx, j, err := s.lockObjectCapacityJob(ctx, id)
	if err != nil {
		return j, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := time.Now().UTC()
	if !validObjectCapacityFinish(j, token, 0, 0, now) || j.InventoryScope != ObjectInventoryAllVersions || j.ScannedPages >= api.ObjectStorageInventoryMaxPages {
		return j, ErrConflict
	}
	ready, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(j.BucketID))
	if err != nil {
		return j, err
	}
	if ready.Pending > 0 || ready.Unsafe || ready.Multipart {
		return j, ErrConflict
	}
	for _, item := range items {
		j.ScannedBytes = boundedObjectAdd(j.ScannedBytes, item.Bytes)
	}
	j.ScannedVersions += int64(len(items))
	j.ScannedPages++
	if j.ScannedBytes > api.MaxObjectStoragePolicyValue || j.ScannedVersions > api.ObjectStorageInventoryMaxPages*api.ObjectVersionInventoryPageSize || next != "" && j.ScannedPages == api.ObjectStorageInventoryMaxPages {
		return j, ErrConflict
	}
	q := sqlc.New()
	raw, err := json.Marshal(items)
	if err != nil {
		return j, ErrConflict
	}
	if len(items) > 0 {
		if _, err = q.ObjectVersionInventoryEntriesInsert(ctx, tx, sqlc.ObjectVersionInventoryEntriesInsertParams{JobID: mustPgUUID(id), Items: raw}); err != nil {
			return j, mapErr(err)
		}
	}
	if next != "" {
		if err = q.ObjectVersionInventoryCursorInsert(ctx, tx, sqlc.ObjectVersionInventoryCursorInsertParams{JobID: mustPgUUID(id), CursorHash: objectKeyHash(next)}); err != nil {
			return j, mapErr(err)
		}
		j.InventoryCursor = next
		j.State = "waiting"
		j.Token = ""
		j.LeaseUntil = time.Time{}
		j.RetryAt = now
		j.UpdatedAt = now
		if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
			return j, err
		}
		return j, tx.Commit(ctx)
	}
	j.InventoryCursor = ""
	j.InventoryVerified = true
	if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
		return j, err
	}
	n, err := q.ObjectVersionCapacityRebase(ctx, tx, sqlc.ObjectVersionCapacityRebaseParams{BucketID: mustPgUUID(j.BucketID), Bytes: j.ScannedBytes, Objects: j.ScannedVersions})
	if err != nil {
		return j, mapErr(err)
	}
	if n != 1 {
		return j, ErrConflict
	}
	if err = q.ObjectCapacityDeleteGrants(ctx, tx, mustPgUUID(j.BucketID)); err != nil {
		return j, err
	}
	if err = q.ObjectCapacityDeleteWrites(ctx, tx, mustPgUUID(j.BucketID)); err != nil {
		return j, err
	}
	if err = q.ObjectInventorySample(ctx, tx, sqlc.ObjectInventorySampleParams{BucketID: mustPgUUID(j.BucketID), Token: token}); err != nil {
		return j, err
	}
	if err = clearObjectVersionInventory(ctx, tx, id); err != nil {
		return j, err
	}
	j = completeObjectCapacityJob(j, j.ScannedBytes, j.ScannedVersions, now)
	if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}

func clearObjectVersionInventory(ctx context.Context, db sqlc.DBTX, id string) error {
	q := sqlc.New()
	if err := q.ObjectVersionInventoryEntriesDelete(ctx, db, mustPgUUID(id)); err != nil {
		return err
	}
	return q.ObjectVersionInventoryCursorsDelete(ctx, db, mustPgUUID(id))
}
