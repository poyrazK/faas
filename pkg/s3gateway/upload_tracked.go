package s3gateway

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func validGatewayETag(etag string) bool {
	return strings.TrimSpace(etag) != "" && len(etag) <= api.MaxObjectWriteETagBytes && !strings.ContainsAny(etag, "\r\n")
}

func (h *Handler) performTrackedGatewayPut(w http.ResponseWriter, r *http.Request, req requestContext, key string, file *os.File, metadata objectstorage.ObjectMetadata) {
	st, c, ok := h.admitGatewayPut(w, r, req, key, metadata.ContentType)
	if !ok {
		return
	}
	w.Header().Set("X-Gregale-Upload-ID", c.ID)
	dispatched := false
	defer func(parent context.Context) {
		if !dispatched {
			c.Status = "failed"
			c.ErrorCode = "dispatch_failed"
			_, _ = h.finishGatewayPut(parent, st, c)
		}
	}(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), api.ObjectTransferTimeout)
	defer cancel()
	upstream, err := h.gatewayPutRequest(ctx, r, req, key, file, metadata, c.ID)
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	intent, ok := h.dispatchGatewayPut(w, r, req, st, c)
	if !ok {
		return
	}
	c = intent
	dispatched = true
	// Disable redirects even when an injected client permits them. No replayable body.
	client := *h.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(upstream) // #nosec G704 -- The immutable registry backend signs the URL; customers supply only the object key and metadata.
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	h.completeGatewayPut(w, r, req, st, c, response)
}

func (h *Handler) completeGatewayPut(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectTrackedGatewayUploadStore, c state.ObjectUploadCompletion, response *http.Response) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
			c.Status = "failed"
			c.ErrorCode = "provider_write_rejected"
			if _, err := h.finishGatewayPut(r.Context(), st, c); err != nil {
				h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
				return
			}
		}
		h.providerHTTPError(w, r, req, response.StatusCode, c.Key)
		return
	}
	c.ETag = response.Header.Get("ETag")
	if !validGatewayETag(c.ETag) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	c.Status = "completed"
	done, err := h.finishGatewayPut(r.Context(), st, c)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	w.Header().Set("ETag", done.ETag)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) finishGatewayPut(parent context.Context, st state.ObjectTrackedGatewayUploadStore, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), api.ObjectUploadSettlementTimeout)
	defer cancel()
	done, err := st.FinishTrackedObjectUpload(ctx, c)
	if err != nil {
		h.log.Warn("S3 write settlement deferred", "bucket_id", c.BucketID, "request_id", c.RequestID)
	}
	return done, err
}

func (h *Handler) gatewayPutRequest(ctx context.Context, r *http.Request, req requestContext, key string, file *os.File, metadata objectstorage.ObjectMetadata, receipt string) (*http.Request, error) {
	sign := objectstorage.SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &r.ContentLength, ContentType: metadata.ContentType, ExpiresIn: int64(api.ObjectGatewayPutURLTTL.Seconds()), CacheControl: metadata.CacheControl, ContentDisposition: metadata.ContentDisposition, ContentEncoding: metadata.ContentEncoding, ContentLanguage: metadata.ContentLanguage, Metadata: metadata.Metadata, Tags: metadata.Tags}
	var signed objectstorage.SignedRequest
	var err error
	if receipt != "" {
		signer, ok := req.provider.(objectstorage.TrackedObjectPresigner)
		if !ok {
			return nil, objectstorage.ErrUnsupported
		}
		signed, err = signer.PresignTrackedPut(ctx, req.bucket.PhysicalName, sign, writeConditions(r), receipt)
	} else {
		signed, err = presignConditionalPut(ctx, req.provider, req.bucket.PhysicalName, sign, writeConditions(r))
	}
	if err != nil {
		return nil, err
	}
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.URL, file)
	if err != nil {
		return nil, objectstorage.ErrUnavailable
	}
	upstream.ContentLength = r.ContentLength
	upstream.GetBody = nil
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	return upstream, nil
}

func (h *Handler) admitGatewayPut(w http.ResponseWriter, r *http.Request, req requestContext, key, contentType string) (state.ObjectTrackedGatewayUploadStore, state.ObjectUploadCompletion, bool) {
	st, ok := h.store.(state.ObjectTrackedGatewayUploadStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return nil, state.ObjectUploadCompletion{}, false
	}
	c, err := st.BeginTrackedGatewayUpload(r.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: req.bucket.AccountID, AppID: req.bucket.AppID, BucketID: req.bucket.ID, SubjectID: req.credential.ID, Key: key, Bytes: r.ContentLength, ContentType: contentType, RequestID: req.requestID, Status: "pending"}, h.registry.Accounting)
	return st, c, h.writeAdmissionError(w, r, req, err)
}
func (h *Handler) dispatchGatewayPut(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectTrackedGatewayUploadStore, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, bool) {
	if !h.recordProviderRequest(w, r, req) {
		return c, false
	}
	intent, err := st.DispatchTrackedObjectUpload(r.Context(), c.AccountID, c.BucketID, c.ID)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return c, false
	}
	return intent, true
}
