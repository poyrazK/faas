package objectstorageactivity

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Tag records only prepared writes. Local refusals and read-only preparation
// never create writers; uncertain mutations retain their capture barrier.
func Tag(ctx context.Context, store any, bucket state.ObjectBucket, service objectstorage.TaggingService, method, key, selector string, tags map[string]string) (api.ObjectTaggingResult, error) {
	service.Mutate = func(ctx context.Context, call func(context.Context) (api.ObjectTaggingResult, error)) (api.ObjectTaggingResult, error) {
		receipt, err := Begin(ctx, store, bucket, state.ObjectBucketMutationRequest)
		if err != nil {
			return api.ObjectTaggingResult{}, err
		}
		out, err := call(ctx)
		if err == nil || errors.Is(err, objectstorage.ErrWriteRejected) {
			err = errors.Join(err, Finish(ctx, store, receipt))
		}
		return out, err
	}
	return service.Do(ctx, bucket, method, key, selector, tags)
}
