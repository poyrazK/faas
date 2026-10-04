package s3gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) mutate(ctx context.Context, req requestContext, call func(context.Context) error) error {
	return objectstorageactivity.Run(ctx, h.store, req.bucket, call)
}

// The signed provider URL stays private to the proxy. Reserve the writer
// immediately before sending the PUT, after request/body validation. HTTP
// errors, asynchronous acceptance and lost responses are not drain evidence.
func (h *Handler) doMutationRequest(upstream *http.Request, req requestContext) (*http.Response, error) {
	receipt, err := objectstorageactivity.Begin(upstream.Context(), h.store, req.bucket, state.ObjectBucketMutationRequest)
	if err != nil {
		return nil, err
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		return response, err
	}
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		if err := objectstorageactivity.Finish(upstream.Context(), h.store, receipt); err != nil {
			h.closeResponseBody(response.Body, req.requestID)
			return nil, err
		}
	}
	return response, nil
}
