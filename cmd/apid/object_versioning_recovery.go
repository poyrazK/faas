package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectBucketVersioning(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectBucketVersioningStore)
	if !ok {
		return nil
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	rows, err := st.DueObjectBucketVersioning(ctx, api.ObjectBucketVersioningBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		b, e := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
		if e != nil {
			return e
		}
		if s.objectStorage == nil {
			return objectstorage.ErrConfiguration
		}
		backend, e := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
		if e != nil {
			continue
		}
		provider, ok := backend.Provider.(objectstorage.BucketVersioningProvider)
		if !ok {
			continue
		}
		metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
		if !ok {
			return objectstorage.ErrConfiguration
		}
		svc := objectstorage.BucketVersioningService{Store: st, Provider: provider, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}
		j, e := svc.Reconcile(ctx, b)
		if errors.Is(e, state.ErrConflict) || errors.Is(e, state.ErrNotFound) {
			continue
		}
		outcome := j.State
		if e != nil {
			outcome = "deferred"
		}
		if observe != nil {
			observe("versioning", outcome)
		}
		if e == nil && j.State == "ready" {
			s.audit.Emit(ctx, "object_storage.versioning_ready", &b.AccountID, map[string]any{"bucket_id": b.ID, "status": j.ObservedStatus, "revision": j.Revision, "inventory_id": j.CapacityJobID})
		}
	}
	return nil
}
