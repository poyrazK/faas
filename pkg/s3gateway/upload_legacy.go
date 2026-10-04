package s3gateway

import (
	"context"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) performLegacyGatewayPut(w http.ResponseWriter, r *http.Request, req requestContext, key string, file *os.File, metadata objectstorage.ObjectMetadata) {
	settle, admitted := h.beginTrackedWrite(w, r, req, key, r.ContentLength)
	if !admitted {
		return
	}
	dispatched := false
	defer func(parent context.Context) {
		if !dispatched {
			settle(parent)
		}
	}(r.Context())
	transferCtx, cancel := context.WithTimeout(r.Context(), h.transferTimeout)
	defer cancel()
	upstream, err := h.gatewayPutRequest(transferCtx, r, req, key, file, metadata, state.ObjectUploadCompletion{})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	dispatched = true
	response, err := h.doMutationRequest(upstream, req)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
			settle(r.Context())
		}
		h.providerHTTPError(w, r, req, response.StatusCode, key)
		return
	}
	etag := response.Header.Get("ETag")
	if !validGatewayETag(etag) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	settle(r.Context())
	w.Header().Set("ETag", etag)
	if !h.publicVersionHeader(w, r, req, key, response.Header.Get("X-Amz-Version-Id"), false) {
		return
	}
	w.WriteHeader(http.StatusOK)
}
