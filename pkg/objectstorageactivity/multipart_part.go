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

// DispatchMultipartPartCopy records exact copy intent in the same transaction
// that claims dispatch. A bound journal lacking this capability cannot downgrade
// a copy into an ordinary part write.
func DispatchMultipartPartCopy(ctx context.Context, store, journal any, b state.ObjectBucket, id string, part int32, token string, intent state.ObjectMultipartPartCopyIntent) (state.ObjectBucketMutation, error) {
	if st, ok := journal.(state.ObjectMultipartPartCopyMutationStore); ok {
		return st.DispatchObjectMultipartPartCopyMutation(ctx, b, id, part, token, intent)
	}
	if _, ok := journal.(state.ObjectMultipartPartMutationStore); ok {
		return state.ObjectBucketMutation{}, state.ErrConflict
	}
	return Begin(ctx, store, b, state.ObjectBucketMutationRequest)
}

// A bound PUT journal must persist intent before IO; missing capability cannot
// silently downgrade the admitted writer to an unqualified ordinary claim.
func DispatchMultipartPartPut(ctx context.Context, store, journal any, b state.ObjectBucket, id string, part int32, token string, i state.ObjectMultipartPartPutIntent) (state.ObjectBucketMutation, error) {
	if st, ok := journal.(state.ObjectMultipartPartPutMutationStore); ok {
		return st.DispatchObjectMultipartPartPutMutation(ctx, b, id, part, token, i)
	}
	if _, ok := journal.(state.ObjectMultipartPartMutationStore); ok {
		return state.ObjectBucketMutation{}, state.ErrConflict
	}
	return Begin(ctx, store, b, state.ObjectBucketMutationRequest)
}

func ObserveMultipartPartBody(ctx context.Context, journal any, r state.ObjectBucketMutation, digest string) error {
	st, ok := journal.(state.ObjectMultipartPartPutMutationStore)
	if !ok || r.MultipartPartWriterID == "" {
		return state.ErrConflict
	}
	observationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectMutationObservationTimeout)
	defer cancel()
	return st.ObserveObjectMultipartPartBody(observationCtx, r, digest)
}
