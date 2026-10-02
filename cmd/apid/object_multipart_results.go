package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) executeObjectMultipartResult(ctx context.Context, uploads state.ObjectMultipartUploadStore, bucket state.ObjectBucket, u state.ObjectMultipartUpload, provider objectstorage.Provider) error {
	store, ok := uploads.(state.ObjectMultipartCompletionStore)
	if !ok {
		return objectstorage.ErrConfiguration
	}
	if err := store.DispatchObjectMultipartCompletion(ctx, u); err != nil {
		return err
	}
	parts := make([]objectstorage.CompletedPart, 0, len(u.Parts))
	for _, part := range u.Parts {
		parts = append(parts, objectstorage.CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag})
	}
	result, err := objectstorage.CompleteMultipartWithResult(ctx, provider, bucket.PhysicalName, objectstorage.MultipartCompleteRequest{
		SessionID: u.ID, Key: u.Key, ProviderUploadID: u.ProviderUploadID, SizeBytes: u.SizeBytes, Parts: parts,
		Recovering: u.CompletionDispatched, RecoveryCursor: u.CompletionRecoveryCursor,
		BeforeRequest: func(ctx context.Context) error {
			metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
			if !ok {
				return objectstorage.ErrConfiguration
			}
			return metrics.RecordObjectStorageProviderRequest(ctx, bucket.ID, time.Now().UTC())
		},
	}, u.CompletionConditions)
	proof := state.ObjectMultipartCompletionResult{ETag: result.ETag, ProviderVersionID: result.ProviderVersionID, RecoveryCursor: result.RecoveryCursor, VersionsObserved: result.VersionsObserved}
	if err != nil {
		return s.deferObjectMultipartResult(ctx, store, u, proof, err)
	}
	if err = finishObjectMultipartOperation(ctx, func(ctx context.Context) error {
		_, e := store.FinishObjectMultipartCompletion(ctx, u, proof)
		return e
	}); err != nil {
		return s.deferObjectMultipartResult(ctx, store, u, proof, err)
	}
	s.audit.Emit(ctx, "object_storage.multipart_upload_completed", &u.AccountID, map[string]any{"app_id": u.AppID, "bucket_id": u.BucketID, "upload_id": u.ID})
	return nil
}

func (s *server) deferObjectMultipartResult(ctx context.Context, store state.ObjectMultipartCompletionStore, u state.ObjectMultipartUpload, result state.ObjectMultipartCompletionResult, cause error) error {
	code := objectstorage.MultipartCompletionFailureCode(cause)
	if code != "" && u.State == state.ObjectMultipartCompletingConditional {
		if err := finishObjectMultipartOperation(ctx, func(ctx context.Context) error {
			return store.RejectObjectMultipartCompletionResult(ctx, u, result, code)
		}); err != nil {
			return err
		}
		s.audit.Emit(ctx, "object_storage.multipart_completion_rejected", &u.AccountID, map[string]any{"app_id": u.AppID, "bucket_id": u.BucketID, "upload_id": u.ID, "error_code": code})
		return cause
	}
	code, delay := objectRetryPolicy(cause, u.AttemptCount)
	if err := finishObjectMultipartOperation(ctx, func(ctx context.Context) error {
		return store.RetryObjectMultipartCompletion(ctx, u, result, code, delay)
	}); err != nil {
		return err
	}
	s.log.Warn("object storage multipart completion deferred", "bucket_id", u.BucketID, "upload_id", u.ID, "error_code", code, "attempt", u.AttemptCount, "retry_in", delay)
	return cause
}
