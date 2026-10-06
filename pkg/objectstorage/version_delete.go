package objectstorage

import (
	"context"
	"net/http"

	"github.com/google/uuid"

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

// DeleteOwnedObjectVersion uses the same durable fence as ordinary deletion.
// The stable public reference selects the immutable native version; completion
// never refunds quota. Ingresses expose caller-owned receipt IDs separately.
func DeleteOwnedObjectVersion(ctx context.Context, st state.ObjectVersionReferenceStore, provider Provider, b state.ObjectBucket, key, id string, before func(context.Context) error) (api.ObjectVersionDeleteResult, error) {
	if !ValidKey(key) || !state.ValidObjectVersionID(id) {
		return api.ObjectVersionDeleteResult{}, ErrInvalid
	}
	if id == "null" {
		return api.ObjectVersionDeleteResult{}, ErrUnsupported
	}
	journal, ok := st.(state.ObjectDeletionStore)
	if !ok {
		return api.ObjectVersionDeleteResult{}, ErrUnsupported
	}
	j, err := (DeletionService{Store: journal, Provider: provider, BeforeRequest: before}).Start(ctx, b, key, id, uuid.NewString(), api.ObjectStoragePolicy{})
	if err == nil && j.State != "completed" {
		err = ErrUnavailable
	}
	return api.ObjectVersionDeleteResult{VersionID: j.VersionID, DeleteMarker: j.DeleteMarker}, err
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
