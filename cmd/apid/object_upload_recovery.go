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

func (s *server) reconcileObjectUploads(ctx context.Context, observe func(string, string)) error {
	st, ok := s.store.(state.ObjectTrackedUploadStore)
	if !ok {
		return nil
	}
	rows, err := st.DueTrackedObjectUploads(ctx, api.ObjectUploadRecoveryBatch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		c, e := st.ClaimTrackedObjectUploadRecovery(ctx, row.AccountID, row.BucketID, row.ID, uuid.NewString())
		if errors.Is(e, state.ErrConflict) || errors.Is(e, state.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		outcome := "failed"
		if c.WritePhase == state.ObjectUploadDispatched {
			outcome, e = s.confirmObjectUpload(ctx, st, c)
			if e != nil {
				return e
			}
		}
		if observe != nil {
			operation := "upload"
			if c.Origin == "gateway" {
				operation = "gateway_put"
			} else if c.Origin == "gateway_copy" {
				operation = "gateway_copy"
			}
			observe(operation, outcome)
		}
	}
	return nil
}
func (s *server) confirmObjectUpload(ctx context.Context, st state.ObjectTrackedUploadStore, c state.ObjectUploadCompletion) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, api.ObjectUploadRecoveryProbeTimeout)
	result, err := s.probeObjectUpload(probeCtx, c)
	cancel()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer finishCancel()
	if err == nil {
		c.Status = "completed"
		c.ETag = result.ETag
		c.ErrorCode = ""
		_, err = st.FinishTrackedObjectUploadRecovery(finishCtx, c)
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			return "stale", nil
		}
		return "completed", err
	}
	code := "provider_write_uncertain"
	if errors.Is(err, objectstorage.ErrConfiguration) || errors.Is(err, objectstorage.ErrUnsupported) {
		code = "configuration"
	}
	retryErr := st.RetryTrackedObjectUploadRecovery(finishCtx, c, code)
	if errors.Is(retryErr, state.ErrConflict) || errors.Is(retryErr, state.ErrNotFound) {
		return "stale", nil
	}
	return "deferred", retryErr
}
func (s *server) probeObjectUpload(ctx context.Context, c state.ObjectUploadCompletion) (objectstorage.UploadResult, error) {
	if s.objectStorage == nil {
		return objectstorage.UploadResult{}, objectstorage.ErrConfiguration
	}
	buckets, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return objectstorage.UploadResult{}, objectstorage.ErrConfiguration
	}
	b, err := buckets.GetObjectBucket(ctx, c.AccountID, c.AppID, c.BucketID)
	if err != nil {
		return objectstorage.UploadResult{}, err
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return objectstorage.UploadResult{}, objectstorage.ErrConfiguration
	}
	writer, ok := backend.Provider.(objectstorage.ObjectWriteConfirmer)
	if !ok {
		return objectstorage.UploadResult{}, objectstorage.ErrUnsupported
	}
	if metrics, ok := s.store.(state.ObjectStorageProviderUsageStore); ok {
		if err = metrics.RecordObjectStorageProviderRequest(ctx, c.BucketID, time.Now().UTC()); err != nil {
			return objectstorage.UploadResult{}, err
		}
	}
	return writer.ConfirmTrackedObject(ctx, b.PhysicalName, c.Key, c.ID, c.Bytes)
}
