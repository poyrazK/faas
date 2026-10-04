package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectCapacity(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectCapacityStore)
	if !ok {
		return nil
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	rows, err := st.DueObjectCapacityReconciliations(ctx, api.ObjectCapacityReconciliationBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		j, e := st.ClaimObjectCapacityReconciliation(ctx, row.ID, uuid.NewString())
		if errors.Is(e, state.ErrConflict) || errors.Is(e, state.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		if j.State == "scanning" {
			b, e := buckets.GetObjectBucket(ctx, j.AccountID, j.AppID, j.BucketID)
			if e == nil {
				j, e = s.scanObjectCapacity(ctx, st, b, j)
			}
			if e != nil {
				finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				retryErr := st.RetryObjectCapacityReconciliation(finishCtx, row.ID, j.Token)
				cancel()
				if retryErr != nil && !errors.Is(retryErr, state.ErrConflict) {
					return retryErr
				}
				if observe != nil {
					observe("capacity", "deferred")
				}
				continue
			}
		}
		if j.FinishedAt != nil {
			s.audit.Emit(ctx, "object_storage.capacity_reconciliation_finished", &j.AccountID, map[string]any{"bucket_id": j.BucketID, "reconciliation_id": j.ID, "state": j.State, "error_code": j.LastErrorCode, "reclaimed_bytes": j.ReclaimedBytes, "reclaimed_keys": j.ReclaimedKeys})
		}
		if observe != nil {
			observe("capacity", j.State)
		}
	}
	return nil
}
func (s *server) scanObjectCapacity(ctx context.Context, st state.ObjectCapacityStore, b state.ObjectBucket, j state.ObjectCapacityReconciliation) (state.ObjectCapacityReconciliation, error) {
	if s.objectStorage == nil {
		return j, objectstorage.ErrConfiguration
	}
	if j.InventoryScope == state.ObjectInventoryAllVersions {
		return s.scanObjectVersionCapacity(ctx, st, b, j)
	}
	scanCtx, cancel := context.WithTimeout(ctx, api.ObjectCapacityInventoryTimeout)
	defer cancel()
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return j, err
	}
	bytes, keys, err := completeObjectInventory(scanCtx, backend.Provider, b.PhysicalName)
	if err != nil {
		return j, err
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	done, err := st.FinishObjectCapacityReconciliation(finishCtx, j.ID, j.Token, bytes, keys)
	if err != nil {
		return j, err
	}
	return done, nil
}

// Complete scans reject partial pages, invalid keys, out-of-order/duplicate
// entries and cursor cycles before any capacity ledger is changed.
func completeObjectInventory(ctx context.Context, p objectstorage.Provider, physical string) (bytes, keys int64, err error) {
	cursor, last := "", ""
	seen := map[string]bool{}
	for range api.ObjectStorageInventoryMaxPages {
		page, e := p.ListObjects(ctx, physical, "", cursor, api.MaxObjectS3ListItems)
		if e != nil {
			return 0, 0, e
		}
		if len(page.Items) > api.MaxObjectS3ListItems || len(page.Items) == 0 && page.NextCursor != "" {
			return 0, 0, objectstorage.ErrInvalid
		}
		for _, o := range page.Items {
			if !objectstorage.ValidKey(o.Key) || last != "" && o.Key <= last || o.Size < 0 || o.Size > api.MaxObjectStoragePolicyValue-bytes {
				return 0, 0, objectstorage.ErrInvalid
			}
			last = o.Key
			bytes += o.Size
			keys++
		}
		if page.NextCursor == "" {
			return bytes, keys, nil
		}
		if seen[page.NextCursor] || len(page.NextCursor) > 8192 {
			return 0, 0, objectstorage.ErrInvalid
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return 0, 0, objectstorage.ErrUnavailable
}
