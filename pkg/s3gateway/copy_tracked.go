package s3gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) performTrackedGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, copier objectstorage.TrackedObjectCopier, copy objectstorage.CopyObjectRequest, conditions objectstorage.CopySourceConditions) {
	st, ok := h.store.(state.ObjectTrackedGatewayCopyStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, copy.DestinationKey)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ObjectTransferTimeout)
	defer cancel()
	if !h.admit(w, r, req, copy.SourceKey, 0, false) || !h.recordProviderRequest(w, r, req) {
		return
	}
	source, err := copier.SnapshotCopySource(ctx, req.bucket.PhysicalName, copy.SourceKey)
	if err != nil {
		h.providerError(w, r, req, err, copy.SourceKey)
		return
	}
	if !h.checkCopySource(w, r, req, copy.SourceKey, source, conditions) {
		return
	}
	c, err := st.BeginTrackedGatewayCopy(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: req.bucket.AccountID, AppID: req.bucket.AppID, BucketID: req.bucket.ID, SubjectID: req.credential.ID, Key: copy.DestinationKey, Bytes: source.SizeBytes, SourceKey: copy.SourceKey, SourceETag: source.ETag, ContentType: gatewayCopyContentType(copy, source), RequestID: req.requestID, Status: "pending"}, h.registry.Accounting)
	if !h.writeAdmissionError(w, r, req, err) {
		return
	}
	w.Header().Set("X-Gregale-Upload-ID", c.ID)
	dispatched := false
	defer func(parent context.Context) {
		if !dispatched {
			c.Status, c.ErrorCode = "failed", "dispatch_failed"
			_, _ = h.finishGatewayPut(parent, st, c)
		}
	}(r.Context())
	intent, ok := h.dispatchGatewayPut(w, r, req, st, c)
	if !ok {
		return
	}
	c, dispatched = intent, true
	var result objectstorage.CopyObjectResult
	if conditions.HasDates() {
		result, err = copier.(objectstorage.DateConditionalTrackedObjectCopier).CopyDateConditionalTrackedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source, conditions)
	} else if conditions.Empty() {
		result, err = copier.CopyTrackedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source)
	} else {
		result, err = copier.(objectstorage.ConditionalTrackedObjectCopier).CopyConditionalTrackedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source, conditions)
	}
	h.completeGatewayCopy(w, r, req, st, c, result, err)
}

func gatewayCopyContentType(copy objectstorage.CopyObjectRequest, source objectstorage.CopySourceSnapshot) string {
	if copy.MetadataDirective == "COPY" {
		return source.Metadata.ContentType
	}
	return copy.Metadata.ContentType
}

func (h *Handler) completeGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectTrackedGatewayCopyStore, c state.ObjectUploadCompletion, result objectstorage.CopyObjectResult, err error) {
	if err != nil {
		if errors.Is(err, objectstorage.ErrWriteRejected) {
			c.Status, c.ErrorCode = "failed", "provider_write_rejected"
			if _, e := h.finishGatewayPut(r.Context(), st, c); e != nil {
				h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
				return
			}
			if errors.Is(err, objectstorage.ErrPreconditionFailed) {
				h.providerHTTPError(w, r, req, http.StatusPreconditionFailed, c.SourceKey)
				return
			}
			h.providerError(w, r, req, err, c.SourceKey)
			return
		}
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	if !validGatewayETag(result.ETag) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	c.Status, c.ETag = "completed", result.ETag
	c.RecoveryVersionsObserved = result.ProviderVersionID != "" && result.ProviderVersionID != "null"
	if _, err = h.finishGatewayPut(r.Context(), st, c); err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	if !h.publicVersionHeader(w, r, req, c.Key, result.ProviderVersionID, false) {
		return
	}
	writeGatewayCopyResult(w, req.requestID, result)
}
