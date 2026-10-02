package objectstorage

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ObjectVersionDeleter permanently removes an immutable native data version or
// marker with one provider attempt. It must reject the mutable null selector.
// An uncertain acknowledgment can safely be retried for this exact identity.
type ObjectVersionDeleter interface {
	DeleteObjectVersion(context.Context, string, string, string) (VersionDeleteResult, error)
}

type VersionDeleteResult struct {
	DeleteMarker bool
}

// A condition or provider protection directive cannot be silently ignored by
// either ingress. Conditional deletion needs its own atomic provider contract.
func ValidateObjectDeleteRequest(r *http.Request) error {
	if r.ContentLength != 0 {
		return ErrInvalid
	}
	for _, name := range []string{"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "X-Amz-If-Match-Last-Modified-Time", "X-Amz-If-Match-Size", "X-Amz-Mfa", "X-Amz-Bypass-Governance-Retention"} {
		if len(r.Header.Values(name)) != 0 {
			return ErrUnsupported
		}
	}
	return nil
}

// DeleteOwnedObjectVersion never accepts a native ID from the customer. Keep
// the durable public reference after deletion so retries across process
// restarts address the same immutable version. Quota is reclaimed separately
// by verified inventory, never by a DELETE acknowledgment.
func DeleteOwnedObjectVersion(ctx context.Context, st state.ObjectVersionReferenceStore, provider Provider, b state.ObjectBucket, key, id string, before func(context.Context) error) (api.ObjectVersionDeleteResult, error) {
	ctx, cancel := context.WithTimeout(ctx, api.ObjectVersionDeleteOperationTimeout)
	defer cancel()
	result := api.ObjectVersionDeleteResult{}
	if !ValidKey(key) || !state.ValidObjectVersionID(id) {
		return result, ErrInvalid
	}
	if id == "null" {
		return result, ErrUnsupported
	}
	p, ok := provider.(ObjectVersionDeleter)
	if !ok || st == nil {
		return result, ErrUnsupported
	}
	native, err := st.ResolveObjectVersion(ctx, b.AccountID, b.ID, key, id)
	if err != nil {
		return result, err
	}
	if !validNativeVersionID(native) || native == "null" {
		return result, ErrUnavailable
	}
	if before != nil {
		if err = before(ctx); err != nil {
			return result, err
		}
	}
	out, err := p.DeleteObjectVersion(ctx, b.PhysicalName, key, native)
	if err != nil {
		return result, err
	}
	return api.ObjectVersionDeleteResult{VersionID: id, DeleteMarker: out.DeleteMarker}, nil
}

// CheckCurrentObjectDelete applies the same accounting guard to the control
// and S3 APIs. Current DELETE cannot bypass native marker admission merely by
// using a different ingress. A configuration transition is also a write fence.
func CheckCurrentObjectDelete(ctx context.Context, store any, b state.ObjectBucket) error {
	if st, ok := store.(state.ObjectVersionInventoryStore); ok {
		status, err := st.ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
		if err != nil {
			return err
		}
		if status.Scope == state.ObjectInventoryAllVersions || status.VersionsObserved || status.NativeScanActive {
			return ErrUnsupported
		}
	}
	if st, ok := store.(state.ObjectBucketVersioningStore); ok {
		j, err := st.GetObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID)
		if err != nil {
			return err
		}
		if j.State != "ready" {
			return state.ErrConflict
		}
	}
	return nil
}
