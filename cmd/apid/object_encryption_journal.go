package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) ensureAdmittedObjectMultipart(ctx context.Context, journal state.ObjectMultipartUploadStore, provider objectstorage.Provider, bucket state.ObjectBucket, upload state.ObjectMultipartUpload, request objectstorage.MultipartCreateRequest) (string, error) {
	call := func(mutationCtx context.Context, dispatch func(context.Context) error) (string, error) {
		return s.ensureUnfencedObjectMultipart(mutationCtx, provider, bucket, upload, request, dispatch)
	}
	if _, capable := provider.(objectstorage.MultipartInitiationProvider); !capable {
		return objectstorageactivity.Execute(ctx, s.store, bucket, func(callCtx context.Context) (string, error) { return call(callCtx, nil) })
	}
	return objectstorageactivity.ExecuteMultipartInitiation(ctx, s.store, journal, bucket, upload, call)
}

func (s *server) ensureUnfencedObjectMultipart(ctx context.Context, provider objectstorage.Provider, bucket state.ObjectBucket, upload state.ObjectMultipartUpload, request objectstorage.MultipartCreateRequest, dispatch func(context.Context) error) (string, error) {
	var err error
	ctx, err = objectstorage.WithObjectWriteProtection(ctx, provider, upload.Protection, func(ctx context.Context) error {
		metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
		if !ok {
			return objectstorage.ErrConfiguration
		}
		return metrics.RecordObjectStorageProviderRequest(ctx, bucket.ID, time.Now().UTC())
	})
	if err != nil {
		return "", err
	}
	if upload.Encryption.Empty() && dispatch == nil {
		return provider.EnsureMultipartUpload(ctx, bucket.PhysicalName, request)
	}
	before := func(ctx context.Context) error {
		metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
		if !ok {
			return objectstorage.ErrConfiguration
		}
		return metrics.RecordObjectStorageProviderRequest(ctx, bucket.ID, time.Now().UTC())
	}
	if !upload.Encryption.Empty() {
		request.BeforeRequest = before
		ctx = objectstorage.WithEncryptionRequestRecorder(ctx, before)
	}
	if dispatch != nil {
		return objectstorage.InitiateMultipartUpload(ctx, provider, bucket.PhysicalName, request, upload.Encryption, dispatch)
	}
	return objectstorage.EnsureMultipartWithEncryption(ctx, provider, bucket.PhysicalName, request, upload.Encryption)
}
