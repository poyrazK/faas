package objectstorageactivity

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// ExecuteMultipartInitiation retains the original receipt through dispatch,
// observation and activation. Unknown replies cannot cause a second creation.
// A nil dispatch callback is reserved for ordinary legacy admission.
func ExecuteMultipartInitiation(ctx context.Context, store, journal any, bucket state.ObjectBucket, u state.ObjectMultipartUpload, call func(context.Context, func(context.Context) error) (string, error)) (string, error) {
	legacy := func(callCtx context.Context) (string, error) { return call(callCtx, nil) }
	st, ok := journal.(state.ObjectMultipartInitiationStore)
	if !ok {
		return Execute(ctx, store, bucket, legacy)
	}
	d, err := st.ReadObjectMultipartInitiation(ctx, u)
	if errors.Is(err, state.ErrNotFound) {
		return Execute(ctx, store, bucket, legacy)
	}
	if err != nil {
		return "", err
	}
	b := d.Receipt.Bucket
	if b.ID != bucket.ID || b.AccountID != bucket.AccountID || b.AppID != bucket.AppID || b.BackendID != bucket.BackendID || b.BackendFingerprint != bucket.BackendFingerprint || b.PhysicalName != bucket.PhysicalName {
		return "", objectstorage.ErrConfiguration
	}
	if d.Dispatched {
		if d.ProviderUploadID != "" {
			return d.ProviderUploadID, nil
		}
		// No listing, timeout or new lease proves ownership of an unknown native
		// upload. Retain the original receipt until stronger proof is available.
		return "", objectstorage.ErrUnavailable
	}
	id, err := call(ctx, func(dispatchCtx context.Context) error {
		return st.DispatchObjectMultipartInitiation(dispatchCtx, u)
	})
	if err != nil {
		return "", err
	}
	observeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectMutationObservationTimeout)
	defer cancel()
	if err := st.ObserveObjectMultipartInitiation(observeCtx, u, id); err != nil {
		return "", err
	}
	return id, nil
}
