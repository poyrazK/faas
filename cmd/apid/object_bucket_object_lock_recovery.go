package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectBucketObjectLock(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectBucketObjectLockStore)
	buckets, owned := s.store.(state.ObjectBucketStore)
	if !ok || !owned {
		return nil
	}
	rows, err := st.DueObjectBucketObjectLock(ctx, api.ObjectBucketObjectLockBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = s.reconcileBucketObjectLockRow(ctx, buckets, st, row, observe); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) reconcileBucketObjectLockRow(ctx context.Context, buckets state.ObjectBucketStore, st state.ObjectBucketObjectLockStore, row state.ObjectBucketObjectLock, observe func(string, string)) error {
	b, err := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.objectStorage == nil {
		return s.deferBucketObjectLockRow(ctx, st, row)
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return s.deferBucketObjectLockRow(ctx, st, row)
	}
	// Accepted work survives disabling ingress or operator capability flags.
	svc, err := s.objectLockService(b, backend, st)
	if err != nil {
		return s.deferBucketObjectLockRow(ctx, st, row)
	}
	j, err := svc.Reconcile(ctx, b)
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return nil
	}
	outcome := j.State
	if err != nil {
		outcome = "deferred"
	}
	if observe != nil {
		observe("object_lock", outcome)
	}
	if err == nil && j.State == "ready" {
		s.audit.Emit(ctx, "object_storage.object_lock_ready", &b.AccountID, map[string]any{"bucket_id": b.ID, "revision": j.Revision})
	}
	return nil
}

func (s *server) deferBucketObjectLockRow(ctx context.Context, st state.ObjectBucketObjectLockStore, row state.ObjectBucketObjectLock) error {
	j, err := st.ClaimObjectBucketObjectLock(ctx, row.BucketID, uuid.NewString())
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil || j.Token == "" {
		return err
	}
	return st.RetryObjectBucketObjectLock(ctx, j.BucketID, j.Token, "provider_unsupported")
}
