package objectstorage

import (
	"context"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This helper lives with the upload data plane because objectstorageactivity
// imports objectstorage; importing that wrapper here would create a cycle.
// Both data planes use the same durable source writer/fence store primitives.
type uploadWriterGuard struct {
	store      state.ObjectBucketMutationStore
	receipt    state.ObjectBucketMutation
	dispatched bool
}

func (h *uploadHandler) admitTrackedObjectWrite(ctx context.Context, b state.ObjectBucket) (*uploadWriterGuard, error) {
	st, ok := h.buckets.(state.ObjectBucketMutationStore)
	if !ok {
		return nil, ErrUnavailable
	}
	receipt, err := st.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		return nil, err
	}
	return &uploadWriterGuard{store: st, receipt: receipt}, nil
}

func (g *uploadWriterGuard) finish(ctx context.Context) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectMutationObservationTimeout)
	defer cancel()
	return g.store.FinishObjectBucketMutation(finishCtx, g.receipt)
}

func (g *uploadWriterGuard) finishUnsent(ctx context.Context) error {
	if g.dispatched {
		return nil
	}
	// The current request has not called the provider. This is concrete local
	// non-dispatch evidence, not a timeout or an inference from missing data.
	return g.finish(ctx)
}

func (g *uploadWriterGuard) write(ctx context.Context, writer ObjectWriter, key string, body io.Reader, size int64, metadata ObjectMetadata) (UploadResult, error) {
	g.dispatched = true
	result, err := writer.WriteObject(ctx, g.receipt.Bucket.PhysicalName, key, body, size, metadata)
	if err != nil {
		return UploadResult{}, err
	}
	if err := g.finish(ctx); err != nil {
		return UploadResult{}, fmt.Errorf("record upload route mutation completion: %w", err)
	}
	return result, nil
}
