package object

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ReconcileObjectBucketEncryption(ctx context.Context, observe func(string, string)) error {
	st, ok := s.Host.Store().(state.ObjectBucketEncryptionStore)
	buckets, owned := s.Host.Store().(state.ObjectBucketStore)
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

func (s *Service) reconcileBucketEncryptionRow(ctx context.Context, buckets state.ObjectBucketStore, st state.ObjectBucketEncryptionStore, row state.ObjectBucketEncryption, observe func(string, string)) error {
	b, err := buckets.GetObjectBucket(ctx, row.AccountID, row.AppID, row.BucketID)
	if err != nil {
		return err
	}
	if s.Host.ObjectStorage() == nil {
		return objectstorage.ErrConfiguration
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return s.deferBucketEncryptionRow(ctx, st, row)
	}
	provider, supported := backend.Provider.(objectstorage.BucketEncryptionProvider)
	if !supported {
		return s.deferBucketEncryptionRow(ctx, st, row)
	}
	metrics, measured := s.Host.Store().(state.ObjectStorageProviderUsageStore)
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
		s.Host.EmitAudit(ctx, "object_storage.encryption_ready", &b.AccountID, map[string]any{"bucket_id": b.ID, "revision": j.Revision})
	}
	return nil
}

func (s *Service) deferBucketEncryptionRow(ctx context.Context, st state.ObjectBucketEncryptionStore, row state.ObjectBucketEncryption) error {
	j, err := st.ClaimObjectBucketEncryption(ctx, row.BucketID, uuid.NewString())
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return st.RetryObjectBucketEncryption(ctx, j.BucketID, j.Token)
}
