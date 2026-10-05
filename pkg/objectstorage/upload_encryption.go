package objectstorage

import (
	"context"
	"io"

	"github.com/onebox-faas/faas/pkg/state"
)

func (h *uploadHandler) writeCapturedRouteUpload(ctx context.Context, st state.ObjectTrackedUploadStore, writer TrackedObjectWriter, bucket state.ObjectBucket, c *state.ObjectUploadCompletion, body io.Reader) (UploadResult, error) {
	metadata := ObjectMetadata{ContentType: c.ContentType}
	if c.Encryption.Empty() {
		return writer.WriteTrackedObject(ctx, bucket.PhysicalName, c.Key, c.ID, body, c.Bytes, metadata)
	}
	encrypted, ok := writer.(ObjectEncryptionProvider)
	if !ok {
		return UploadResult{}, ErrWriteRejected
	}
	before := func(ctx context.Context) error {
		if h.requestMetrics == nil {
			return ErrConfiguration
		}
		return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, bucket.ID, h.now())
	}
	ctx = WithEncryptionRequestRecorder(ctx, before)
	ctx = WithEncryptionWriteRecorder(ctx, func(ctx context.Context) error {
		if err := before(ctx); err != nil {
			return err
		}
		dispatched, err := st.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
		if err == nil {
			*c = dispatched
		}
		return err
	})
	return encrypted.WriteEncryptedObject(ctx, bucket.PhysicalName, c.Key, c.ID, body, c.Bytes, metadata, c.Encryption)
}
