package s3gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reserve the writer immediately before provider IO. The caller owns receipt
// settlement after validating the complete synchronous acknowledgment. A status
// code alone, asynchronous acceptance or a lost reply cannot drain the writer.
func (h *Handler) doMutationRequest(upstream *http.Request, req requestContext) (*http.Response, state.ObjectBucketMutation, error) {
	receipt, err := objectstorageactivity.Begin(upstream.Context(), h.store, req.bucket, state.ObjectBucketMutationRequest)
	if err != nil {
		return nil, state.ObjectBucketMutation{}, err
	}
	response, err := h.client.Do(upstream)
	return response, receipt, err
}
