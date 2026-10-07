// Package objectstorageactivity records source writers before provider IO.
package objectstorageactivity

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func Begin(ctx context.Context, store any, bucket state.ObjectBucket, kind string) (state.ObjectBucketMutation, error) {
	st, ok := store.(state.ObjectBucketMutationStore)
	if !ok {
		return state.ObjectBucketMutation{}, objectstorage.ErrUnavailable
	}
	return st.BeginObjectBucketMutation(ctx, bucket, kind)
}

// Finish is called only after a validated synchronous acknowledgment or proof
// that the write was rejected without mutation. It intentionally
// outlives request cancellation for a bounded database acknowledgement. Errors
// and lost provider replies leave the writer outstanding; no TTL drains it.
func Finish(ctx context.Context, store any, receipt state.ObjectBucketMutation) error {
	st, ok := store.(state.ObjectBucketMutationStore)
	if !ok {
		return objectstorage.ErrUnavailable
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectMutationObservationTimeout)
	defer cancel()
	if err := st.FinishObjectBucketMutation(finishCtx, receipt); err != nil {
		return fmt.Errorf("record object mutation completion: %w", err)
	}
	return nil
}

func Execute[T any](ctx context.Context, store any, bucket state.ObjectBucket, call func(context.Context) (T, error)) (T, error) {
	var zero T
	receipt, err := Begin(ctx, store, bucket, state.ObjectBucketMutationRequest)
	if err != nil {
		return zero, err
	}
	value, err := call(ctx)
	if err != nil {
		return value, err
	}
	if err := Finish(ctx, store, receipt); err != nil {
		return value, err
	}
	return value, nil
}

func Run(ctx context.Context, store any, bucket state.ObjectBucket, call func(context.Context) error) error {
	_, err := Execute(ctx, store, bucket, func(callCtx context.Context) (struct{}, error) { return struct{}{}, call(callCtx) })
	return err
}

// ExecuteUpload reuses an original journal's pinned receipt. Journal settlement,
// rather than provider acknowledgement, retires it atomically. It never admits
// a new write or adopts an unbound legacy request while capture is held.
func ExecuteUpload[T any](ctx context.Context, store any, bucket state.ObjectBucket, c state.ObjectUploadCompletion, call func(context.Context) (T, error)) (T, error) {
	var zero T
	st, ok := store.(state.ObjectTrackedUploadMutationStore)
	if !ok {
		return zero, objectstorage.ErrUnavailable
	}
	receipt, err := st.ReadTrackedObjectUploadMutation(ctx, c)
	if err != nil {
		return zero, err
	}
	b := receipt.Bucket
	if b.ID != bucket.ID || b.AccountID != bucket.AccountID || b.AppID != bucket.AppID || b.BackendID != bucket.BackendID || b.BackendFingerprint != bucket.BackendFingerprint || b.PhysicalName != bucket.PhysicalName {
		return zero, objectstorage.ErrConfiguration
	}
	return call(ctx)
}

// ExecuteMultipart resumes original completion or abort. Only terminal journal
// settlement retires the receipt; provider ACK alone does not establish it.
func ExecuteMultipart[T any](ctx context.Context, store, journal any, bucket state.ObjectBucket, u state.ObjectMultipartUpload, call func(context.Context) (T, error)) (T, error) {
	var zero T
	st, ok := journal.(state.ObjectMultipartMutationStore)
	if !ok {
		return Execute(ctx, store, bucket, call)
	}
	receipt, err := st.ReadObjectMultipartMutation(ctx, u)
	if errors.Is(err, state.ErrNotFound) {
		// Legacy journals have no original binding. Keep ordinary admission and
		// leave any pre-existing unknown receipt untouched; a hold rejects this.
		return Execute(ctx, store, bucket, call)
	}
	if err != nil {
		return zero, err
	}
	b := receipt.Bucket
	if b.ID != bucket.ID || b.AccountID != bucket.AccountID || b.AppID != bucket.AppID || b.BackendID != bucket.BackendID || b.BackendFingerprint != bucket.BackendFingerprint || b.PhysicalName != bucket.PhysicalName {
		return zero, objectstorage.ErrConfiguration
	}
	return call(ctx)
}

func RunMultipart(ctx context.Context, store, journal any, bucket state.ObjectBucket, u state.ObjectMultipartUpload, call func(context.Context) error) error {
	_, err := ExecuteMultipart(ctx, store, journal, bucket, u, func(callCtx context.Context) (struct{}, error) { return struct{}{}, call(callCtx) })
	return err
}
