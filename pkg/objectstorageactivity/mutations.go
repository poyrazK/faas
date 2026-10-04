// Package objectstorageactivity records source writers before provider IO.
package objectstorageactivity

import (
	"context"
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

// Finish is called only after synchronous provider success. It intentionally
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
