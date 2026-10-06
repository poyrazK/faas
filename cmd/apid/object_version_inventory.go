package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) scanObjectVersionCapacity(ctx context.Context, st state.ObjectCapacityStore, b state.ObjectBucket, j state.ObjectCapacityReconciliation) (state.ObjectCapacityReconciliation, error) {
	versions, ok := s.store.(state.ObjectVersionInventoryStore)
	if !ok {
		return j, objectstorage.ErrUnsupported
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return j, objectstorage.ErrConfiguration
	}
	provider, ok := backend.Provider.(objectstorage.ObjectVersionInventoryProvider)
	if !ok {
		return j, objectstorage.ErrUnsupported
	}
	metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
	if !ok {
		return j, objectstorage.ErrConfiguration
	}
	scanCtx, cancel := context.WithTimeout(ctx, api.ObjectCapacityInventoryTimeout)
	defer cancel()
	for range api.ObjectVersionInventoryPagesPerSweep {
		if err = metrics.RecordObjectStorageProviderRequest(scanCtx, b.ID, time.Now().UTC()); err != nil {
			return j, err
		}
		page, e := provider.ListObjectVersions(scanCtx, b.PhysicalName, j.InventoryCursor, api.ObjectVersionInventoryPageSize)
		if e != nil {
			return j, e
		}
		records := make([]state.ObjectVersionInventoryRecord, 0, len(page.Items))
		for _, item := range page.Items {
			if !objectstorage.ValidKey(item.Key) || item.ProviderVersionID == "" || item.SizeBytes < 0 {
				return j, objectstorage.ErrInvalid
			}
			hash := sha256.Sum256([]byte(item.Key + "\x00" + item.ProviderVersionID))
			records = append(records, state.ObjectVersionInventoryRecord{Identity: hex.EncodeToString(hash[:]), Bytes: item.SizeBytes})
		}
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
		next, e := versions.StageObjectVersionInventoryPage(finishCtx, j.ID, j.Token, page.NextCursor, records)
		finishCancel()
		if e != nil {
			return j, e
		}
		j = next
		if j.State != "waiting" {
			return j, nil
		}
		if err = scanCtx.Err(); err != nil {
			return j, nil
		} // Progress committed; the next sweep resumes it.
		if j.ScannedPages%api.ObjectVersionInventoryPagesPerSweep == 0 {
			return j, nil
		}
		claimed, e := st.ClaimObjectCapacityReconciliation(scanCtx, j.ID, uuid.NewString())
		if errors.Is(e, state.ErrConflict) || errors.Is(e, state.ErrNotFound) {
			return j, nil
		}
		if e != nil {
			return j, e
		}
		j = claimed
		if j.State != "scanning" {
			return j, nil
		}
	}
	return j, nil
}

// Native inventories use the durable page journal, including periodic refreshes.
func (s *server) queueNativeObjectInventory(ctx context.Context, accounting state.ObjectStorageAccountingStore, b state.ObjectBucket) (bool, error) {
	versions, ok := s.store.(state.ObjectVersionInventoryStore)
	if !ok {
		return false, nil
	}
	status, err := versions.ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
	if err != nil {
		return false, err
	}
	if status.Scope != state.ObjectInventoryAllVersions && !status.VersionsObserved {
		return false, nil
	}
	capacity, ok := s.store.(state.ObjectCapacityStore)
	if !ok {
		return true, objectstorage.ErrConfiguration
	}
	// Advance the existing refresh cadence even when readiness later blocks the
	// job, so an unsafe legacy grant cannot create a new job every sweep.
	err = accounting.ClaimObjectInventory(ctx, b.ID, uuid.NewString())
	if errors.Is(err, state.ErrConflict) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	_, err = capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if errors.Is(err, state.ErrConflict) {
		return true, nil
	}
	return true, err
}
