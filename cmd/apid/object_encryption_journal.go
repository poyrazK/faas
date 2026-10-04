package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) ensureAdmittedObjectMultipart(ctx context.Context, provider objectstorage.Provider, bucket state.ObjectBucket, upload state.ObjectMultipartUpload, request objectstorage.MultipartCreateRequest) (string, error) {
	return objectstorageactivity.Execute(ctx, s.store, bucket, func(mutationCtx context.Context) (string, error) {
		return s.ensureUnfencedObjectMultipart(mutationCtx, provider, bucket, upload, request)
	})
}

func (s *server) ensureUnfencedObjectMultipart(ctx context.Context, provider objectstorage.Provider, bucket state.ObjectBucket, upload state.ObjectMultipartUpload, request objectstorage.MultipartCreateRequest) (string, error) {
	if upload.Encryption.Empty() {
		return provider.EnsureMultipartUpload(ctx, bucket.PhysicalName, request)
	}
	before := func(ctx context.Context) error {
		metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
		if !ok {
			return objectstorage.ErrConfiguration
		}
		return metrics.RecordObjectStorageProviderRequest(ctx, bucket.ID, time.Now().UTC())
	}
	request.BeforeRequest = before
	ctx = objectstorage.WithEncryptionRequestRecorder(ctx, before)
	return objectstorage.EnsureMultipartWithEncryption(ctx, provider, bucket.PhysicalName, request, upload.Encryption)
}
