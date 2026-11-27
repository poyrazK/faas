package objectstorage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RouteDeliveryStore reuses the durable upload journal and captured provider
// mutation. The caller authorizes the app, route and bucket before entering it.
type RouteDeliveryStore interface {
	state.ObjectTrackedUploadStore
	state.ObjectTrackedUploadMutationStore
	state.ObjectStorageProviderUsageStore
}

// DeliverRouteObject makes at most one tracked provider attempt. body must be a
// fully verified, bounded source. Uncertain writes return a pending receipt; the
// existing object upload recovery worker resolves them without reading body.
func DeliverRouteObject(ctx context.Context, registry *Registry, st RouteDeliveryStore, bucket state.ObjectBucket, intent state.ObjectUploadCompletion, body io.Reader) (state.ObjectUploadCompletion, error) {
	if registry == nil || st == nil || !registry.Accounting.Valid() {
		return intent, ErrUnavailable
	}
	backend, err := registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		return intent, err
	}
	writer, ok := backend.Provider.(TrackedObjectWriter)
	if !ok {
		return intent, ErrUnsupported
	}
	locks, ok := st.(state.ObjectBucketObjectLockStore)
	if !ok {
		return intent, ErrUnavailable
	}
	lock, err := locks.GetObjectBucketObjectLock(ctx, bucket.AccountID, bucket.AppID, bucket.ID)
	if err != nil {
		return intent, err
	}
	if (lock.EnabledRequired || lock.NativeEnabledObserved) && !backend.ObjectLock.Enabled {
		return intent, ErrUnsupported
	}
	if lock.ObservedConfiguration != nil && lock.ObservedConfiguration.DefaultRetention != nil && lock.ObservedConfiguration.DefaultRetention.DefaultEventHold != nil && !backend.ObjectLock.EventHolds {
		return intent, ErrUnsupported
	}
	if !intent.Encryption.Empty() && backend.Encryption.VerifySnapshot(bucket.AccountID, intent.Encryption) != nil {
		return intent, ErrConfiguration
	}
	intent.QueueNodeSettlement = QueueOwnedNodeSettlement(backend.Provider)
	intent.Protection.AdmitEventHolds = backend.ObjectLock.EventHolds
	current, created, err := st.BeginTrackedObjectUpload(ctx, intent, registry.Accounting)
	if err != nil {
		return current, err
	}
	if !created {
		if current.RequestFingerprint != intent.RequestFingerprint {
			return current, ErrConflict
		}
		return current, nil
	}
	h := uploadHandler{registry: registry, requestMetrics: st, now: func() time.Time { return time.Now().UTC() }, log: slog.Default()}
	return h.deliverRouteObject(ctx, st, writer, bucket, current, body)
}

func (h *uploadHandler) deliverRouteObject(ctx context.Context, st RouteDeliveryStore, writer TrackedObjectWriter, bucket state.ObjectBucket, c state.ObjectUploadCompletion, body io.Reader) (state.ObjectUploadCompletion, error) {
	if c.Encryption.Empty() {
		if err := RecordGatewayProviderRequest(ctx, st, bucket.ID, h.now(), h.registry.Accounting); err != nil {
			return h.failRouteDelivery(ctx, st, c, "usage_unavailable", err)
		}
		dispatched, err := st.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
		if err != nil {
			return h.failRouteDelivery(ctx, st, c, "dispatch_failed", err)
		}
		c = dispatched
	}
	result, err := h.writeCapturedRouteUpload(ctx, st, writer, bucket, &c, body)
	if err != nil && !errors.Is(err, ErrWriteRejected) || err == nil && !validUploadETag(result.ETag) {
		return c, nil // Never guess an uncertain dispatched write failed.
	}
	c.Status, c.ETag = "completed", result.ETag
	c.VerifiedProtection, c.VerifiedEncryption = result.VerifiedProtection, result.Encryption
	c.ProviderVersionID = result.ProviderVersionID
	c.RecoveryVersionsObserved = result.ProviderVersionID != "" && result.ProviderVersionID != "null"
	if err != nil {
		c.Status, c.ErrorCode, c.ETag, c.ProviderVersionID = "failed", "provider_write_rejected", "", ""
		c.VerifiedProtection, c.VerifiedEncryption = "", api.ObjectEncryption{}
	}
	done, finishErr := h.finishTrackedUpload(ctx, st, c)
	if finishErr != nil {
		c.Status, c.ETag = "pending", ""
		return c, nil //nolint:nilerr // Settlement uncertainty is a durable pending receipt, recovered by polling.
	}
	return done, nil
}

func (h *uploadHandler) failRouteDelivery(ctx context.Context, st RouteDeliveryStore, c state.ObjectUploadCompletion, code string, cause error) (state.ObjectUploadCompletion, error) {
	c.Status, c.ErrorCode = "failed", code
	_, _ = h.finishTrackedUpload(ctx, st, c)
	return c, cause
}
