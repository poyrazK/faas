package objectstorage

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func RecordGatewayProviderRequest(ctx context.Context, metrics state.ObjectStorageProviderUsageStore, bucket string, at time.Time, p api.ObjectStoragePolicy) error {
	if metrics == nil {
		return ErrConfiguration
	}
	if p.GatewaySafety() {
		store, ok := metrics.(state.ObjectStorageGatewayRequestStore)
		if !ok {
			return state.ErrObjectUsageStale
		}
		return store.ReserveObjectStorageGatewayRequest(ctx, bucket, at, p)
	}
	return metrics.RecordObjectStorageProviderRequest(ctx, bucket, at)
}

// ReserveGatewayRead commits a conservative egress reservation before any
// successful GET body leaves Gregale. HEAD/conditional responses have no
// body. Unknown lengths fail closed; cancellations never refund reservations.
func ReserveGatewayRead(ctx context.Context, metrics state.ObjectStorageProviderUsageStore, bucket string, response *http.Response, p api.ObjectStoragePolicy, at time.Time) error {
	store, ok := metrics.(state.ObjectStorageGatewayEgressStore)
	if !ok || response.ContentLength < 0 || response.Uncompressed {
		return state.ErrObjectUsageStale
	}
	return store.ReserveObjectStorageGatewayEgress(ctx, bucket, response.ContentLength, at, p)
}

// RecordDeliveredEgress retains the legacy delivered-byte measurement. The
// detached, bounded write survives a client disconnect, but cannot claim the
// crash-safe reservation semantics of gateway safety mode.
func RecordDeliveredEgress(ctx context.Context, metrics state.ObjectStorageProviderUsageStore, bucket string, bytes int64, at time.Time) error {
	store, ok := metrics.(state.ObjectStorageProviderEgressStore)
	if !ok || bytes == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return store.RecordObjectStorageProviderEgress(ctx, bucket, bytes, at)
}
