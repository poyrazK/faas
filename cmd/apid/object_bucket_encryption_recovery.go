package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectBucketEncryption(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectBucketEncryptionStore)
	buckets, owned := s.store.(state.ObjectBucketStore)
	if !ok || !owned {
		return nil
	}
	rows, err := st.DueObjectBucketEncryption(ctx, api.ObjectBucketEncryptionBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = s.reconcileBucketEncryptionRow(ctx, buckets, st, row, observe); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) reconcileBucketEncryptionRow(ctx context.Context, buckets state.ObjectBucketStore, st state.ObjectBucketEncryptionStore, row state.ObjectBucketEncryption, observe func(string, string)) error {
	b, err := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
	if err != nil {
		return err
	}
	if s.objectStorage == nil {
		return objectstorage.ErrConfiguration
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return s.deferBucketEncryptionRow(ctx, st, row)
	}
	provider, supported := backend.Provider.(objectstorage.BucketEncryptionProvider)
	if !supported {
		return s.deferBucketEncryptionRow(ctx, st, row)
	}
	metrics, measured := s.store.(state.ObjectStorageProviderUsageStore)
	if !measured {
		return objectstorage.ErrConfiguration
	}
	svc := objectstorage.BucketEncryptionService{Store: st, Provider: provider, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}
	j, err := svc.Reconcile(ctx, b)
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return nil
	}
	outcome := j.State
	if err != nil {
		outcome = "deferred"
	}
	if observe != nil {
		observe("encryption", outcome)
	}
	if err == nil && j.State == "ready" {
		s.audit.Emit(ctx, "object_storage.encryption_ready", &b.AccountID, map[string]any{"bucket_id": b.ID, "revision": j.Revision})
	}
	return nil
}

func (s *server) deferBucketEncryptionRow(ctx context.Context, st state.ObjectBucketEncryptionStore, row state.ObjectBucketEncryption) error {
	j, err := st.ClaimObjectBucketEncryption(ctx, row.BucketID, uuid.NewString())
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return st.RetryObjectBucketEncryption(ctx, j.BucketID, j.Token)
}
