package main

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func (s *server) WithOperationArtifactStorage(backend storage.StorageBackend) *server {
	s.operationArtifactStorage = backend
	return s
}

func (s *server) retainOperationArtifact(ctx context.Context, op state.Operation, authority state.OperationExecutionAuthority, req api.OperationArtifactRequest) (string, error) {
	if operationHasArtifactReceipt(op, authority, req.ReportID) {
		return "", nil
	}
	store, ok := s.store.(state.OperationResultBlobStore)
	if !ok || s.operationArtifactStorage == nil {
		return "", objectstorage.ErrUnavailable
	}
	blob, err := store.ReserveOperationArtifact(ctx, op.ID, authority, req)
	if err != nil {
		return "", err
	}
	if blob.State == "retained" {
		return blob.ID, nil
	}
	f, err := s.verifyOperationArtifact(ctx, op, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if !blob.ExpiresAt.After(time.Now()) {
		return "", state.ErrOperationStaleAttempt
	}
	// Each reservation has a fresh key. Parallel report retries cannot replace
	// another upload; only the winning claim-fenced commit pins its copy.
	if err := s.operationArtifactStorage.Put(ctx, blob.StorageKey, f); err != nil {
		return "", err
	}
	return blob.ID, nil
}

func (s *server) readRetainedOperationArtifact(ctx context.Context, op state.Operation, artifact api.OperationResultArtifact, req api.OperationArtifactRequest) (*verifiedOperationArtifact, error) {
	key := op.ArtifactStorageKeys[artifact.ID]
	if key == "" {
		return nil, storage.ErrNotFound
	}
	if s.operationArtifactStorage == nil {
		return nil, objectstorage.ErrUnavailable
	}
	return s.spoolOperationArtifact(op, req, func() (io.ReadCloser, error) { return s.operationArtifactStorage.Get(ctx, key) })
}

func (s *server) runOperationArtifactCleanup(ctx context.Context) {
	ticker := time.NewTicker(api.OperationArtifactCleanupInterval)
	defer ticker.Stop()
	for {
		if err := s.cleanupOperationArtifacts(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.log.Warn("operation artifact cleanup deferred", "error_code", "storage_or_ledger_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) cleanupOperationArtifacts(ctx context.Context, now time.Time) error {
	store, ok := s.store.(state.OperationResultBlobStore)
	if !ok || s.operationArtifactStorage == nil {
		return nil
	}
	for range api.OperationArtifactCleanupBatch {
		callCtx, cancel := context.WithTimeout(ctx, api.OperationArtifactTransferTimeout)
		blob, err := store.ClaimOperationArtifactCleanup(callCtx, uuid.NewString(), now)
		if errors.Is(err, state.ErrNotFound) {
			cancel()
			return nil
		}
		if err != nil {
			cancel()
			return err
		}
		err = s.operationArtifactStorage.Delete(callCtx, blob.StorageKey)
		cancel()
		settleCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), api.OperationExecutionRenewTimeout)
		if err == nil || errors.Is(err, storage.ErrNotFound) {
			err = store.CompleteOperationArtifactCleanup(settleCtx, blob.ID, blob.LeaseToken)
		} else {
			if retryErr := store.RetryOperationArtifactCleanup(settleCtx, blob.ID, blob.LeaseToken, now.Add(api.OperationArtifactCleanupRetry)); retryErr != nil {
				err = retryErr
			}
		}
		finish()
		if err != nil {
			return err
		}
	}
	return nil
}
