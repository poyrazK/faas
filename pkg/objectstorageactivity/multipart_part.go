package objectstorageactivity

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// DispatchMultipartPart claims the original admitted attempt. Legacy stores
// retain ordinary admission, which cannot pass a source capture hold.
func DispatchMultipartPart(ctx context.Context, store, journal any, b state.ObjectBucket, id string, part int32, token string) (state.ObjectBucketMutation, error) {
	if st, ok := journal.(state.ObjectMultipartPartMutationStore); ok {
		return st.DispatchObjectMultipartPartMutation(ctx, b, id, part, token)
	}
	return Begin(ctx, store, b, state.ObjectBucketMutationRequest)
}

// FinishMultipartPart is called only with validated synchronous success or
// qualified rejection. Bound settlement clears transfer and receipt atomically.
func FinishMultipartPart(ctx context.Context, store, journal any, r state.ObjectBucketMutation) error {
	if r.MultipartPartWriterID == "" {
		return Finish(ctx, store, r)
	}
	st, ok := journal.(state.ObjectMultipartPartMutationStore)
	if !ok {
		return state.ErrConflict
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectMutationObservationTimeout)
	defer cancel()
	return st.FinishObjectMultipartPartMutation(finishCtx, r)
}
