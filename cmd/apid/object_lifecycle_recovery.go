package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectLifecycle(ctx context.Context, observe func(string, string)) error {
	if !s.objectStorageEnabled() {
		return nil
	}
	st, ok := s.store.(objectstorage.LifecycleExpirationStore)
	if !ok {
		return nil
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectLifecycleWorkerTimeout)
	defer cancel()
	rows, err := st.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		b, err := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
		if err != nil {
			backend.Provider = nil
		}
		recorder := s.deletionService(b, backend.Provider).BeforeRequest
		svc := objectstorage.LifecycleExpirationService{Store: st, Provider: backend.Provider, BeforeRequest: func(ctx context.Context) error {
			if !s.objectStorageEnabled() {
				return objectstorage.ErrUnavailable
			}
			return recorder(ctx)
		}}
		j, err := svc.Step(ctx, b, s.objectStorage.Accounting)
		outcome := j.State
		if err != nil {
			outcome = "deferred"
		}
		if observe != nil {
			observe("lifecycle", outcome)
		}
		if err == nil && j.State == "completed" {
			s.audit.Emit(ctx, "object_storage.lifecycle_scan_completed", &b.AccountID, map[string]any{"bucket_id": b.ID, "scan_id": j.ID, "revision": j.Revision, "scanned_keys": j.ScannedKeys})
		}
	}
	return nil
}
