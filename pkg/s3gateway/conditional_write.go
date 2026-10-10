package s3gateway

import (
	"context"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 955
func (h *Handler) conditionalWriteContext(ctx context.Context, req requestContext) context.Context {
	return objectstorage.WithConditionalWriteRequestRecorder(ctx, func(ctx context.Context) error {
		if h.requestMetrics == nil || !h.enabled() {
			return objectstorage.ErrConfiguration
		}
		credential, bucket, err := h.store.ResolveObjectS3Credential(ctx, req.credential.AccessKeyID)
		if err != nil || credential.ID != req.credential.ID || bucket.ID != req.bucket.ID || bucket.AccountID != req.bucket.AccountID || bucket.State != "ready" || bucket.BackendID != req.bucket.BackendID || bucket.BackendFingerprint != req.bucket.BackendFingerprint || bucket.PhysicalName != req.bucket.PhysicalName {
			return state.ErrConflict
		}
		if h.registry.Accounting.GatewaySafety() {
			metrics, ok := h.requestMetrics.(state.ObjectStorageGatewayRequestStore)
			if !ok {
				return state.ErrObjectUsageStale
			}
			return metrics.ReserveObjectStorageGatewayRequest(ctx, req.bucket.ID, h.now().UTC(), h.registry.Accounting)
		}
		return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
	})
}
