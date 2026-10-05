package s3gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) performTrackedGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, copier objectstorage.TrackedObjectCopier, copy objectstorage.CopyObjectRequest, conditions objectstorage.CopySourceConditions) {
	st, ok := h.store.(state.ObjectTrackedGatewayCopyStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, copy.DestinationKey)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.transferTimeout)
	defer cancel()
	c, source, sourceID, ok := h.admitGatewayCopy(w, r, req, ctx, st, copier, copy, conditions)
	if !ok {
		return
	}
	w.Header().Set("X-Gregale-Upload-ID", c.ID)
	dispatched, settlementAttempted := false, false
	defer func(parent context.Context) {
		if !dispatched && !settlementAttempted {
			c.Status, c.ErrorCode = "failed", "dispatch_failed"
			_, _ = h.finishGatewayPut(parent, st, c)
		}
	}(r.Context())
	result, err, ok := h.executeAdmittedGatewayCopy(w, r, req, ctx, st, copier, copy, source, conditions, &c, &dispatched)
	if !ok {
		return
	}
	settlementAttempted = true
	h.completeGatewayCopy(w, r, req, st, c, sourceID, result, err)
}

func gatewayCopyContentType(copy objectstorage.CopyObjectRequest, source objectstorage.CopySourceSnapshot) string {
	if copy.MetadataDirective == "COPY" {
		return source.Metadata.ContentType
	}
	return copy.Metadata.ContentType
}

func (h *Handler) completeGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectTrackedGatewayCopyStore, c state.ObjectUploadCompletion, sourceID string, result objectstorage.CopyObjectResult, err error) {
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
	c.VerifiedProtection = result.VerifiedProtection
	c.VerifiedEncryption = result.Encryption
	c.Status, c.ETag = "completed", result.ETag
	c.ProviderVersionID = result.ProviderVersionID
	c.RecoveryVersionsObserved = result.ProviderVersionID != "" && result.ProviderVersionID != "null"
	done, err := h.finishGatewayPut(r.Context(), st, c)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	writeEncryptionHeaders(w.Header(), done.Encryption.Selection)
	if done.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", done.VersionID)
	}
	if sourceID != "" {
		w.Header().Set("X-Amz-Copy-Source-Version-Id", sourceID)
	}
	writeGatewayCopyResult(w, req.requestID, result)
}

func (h *Handler) admitGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, ctx context.Context, st state.ObjectTrackedGatewayCopyStore, copier objectstorage.TrackedObjectCopier, copy objectstorage.CopyObjectRequest, conditions objectstorage.CopySourceConditions) (state.ObjectUploadCompletion, objectstorage.CopySourceSnapshot, string, bool) {
	sourceReq := copySourceContext(req)
	if !h.admit(w, r, sourceReq, copy.SourceKey, 0, false) || !h.recordProviderRequest(w, r, sourceReq) {
		return state.ObjectUploadCompletion{}, objectstorage.CopySourceSnapshot{}, "", false
	}
	source, err := snapshotGatewayCopySource(ctx, sourceReq, copy, copier)
	if err != nil {
		h.providerError(w, r, req, err, copy.SourceKey)
		return state.ObjectUploadCompletion{}, objectstorage.CopySourceSnapshot{}, "", false
	}
	sourceID, ok := h.publicObjectVersionID(w, r, sourceReq, copy.SourceKey, source.ProviderVersionID, false)
	if !ok {
		return state.ObjectUploadCompletion{}, objectstorage.CopySourceSnapshot{}, "", false
	}
	if !h.checkCopySource(w, r, sourceReq, copy.SourceKey, source, conditions) {
		return state.ObjectUploadCompletion{}, objectstorage.CopySourceSnapshot{}, "", false
	}
	intent := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: req.bucket.AccountID, AppID: req.bucket.AppID, BucketID: req.bucket.ID, SubjectID: req.credential.ID, Key: copy.DestinationKey, Bytes: source.SizeBytes, SourceKey: copy.SourceKey, SourceETag: source.ETag, ContentType: gatewayCopyContentType(copy, source), RequestID: req.requestID, Status: "pending", Protection: req.protection.Clone(), Encryption: req.encryption.Clone()}
	if req.copySource != nil {
		intent.SourceBucketID, intent.SourceCopyGrantID = req.copySource.ID, req.copyGrantID
	}
	c, err := st.BeginTrackedGatewayCopy(ctx, intent, h.registry.Accounting)
	if !h.writeAdmissionError(w, r, req, err) {
		return state.ObjectUploadCompletion{}, objectstorage.CopySourceSnapshot{}, "", false
	}
	return c, source, sourceID, true
}

func (h *Handler) executeAdmittedGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, ctx context.Context, st state.ObjectTrackedGatewayCopyStore, copier objectstorage.TrackedObjectCopier, copy objectstorage.CopyObjectRequest, source objectstorage.CopySourceSnapshot, conditions objectstorage.CopySourceConditions, c *state.ObjectUploadCompletion, dispatched *bool) (objectstorage.CopyObjectResult, error, bool) {
	receipt, err := objectstorageactivity.Begin(ctx, h.store, req.bucket, state.ObjectBucketMutationRequest)
	if err != nil {
		return objectstorage.CopyObjectResult{}, err, true
	}
	result, err, ok := h.executeUnfencedAdmittedGatewayCopy(w, r, req, ctx, st, copier, copy, source, conditions, c, dispatched)
	if err == nil && validGatewayETag(result.ETag) {
		err = objectstorageactivity.Finish(ctx, h.store, receipt)
	}
	return result, err, ok
}

func (h *Handler) executeUnfencedAdmittedGatewayCopy(w http.ResponseWriter, r *http.Request, req requestContext, ctx context.Context, st state.ObjectTrackedGatewayCopyStore, copier objectstorage.TrackedObjectCopier, copy objectstorage.CopyObjectRequest, source objectstorage.CopySourceSnapshot, conditions objectstorage.CopySourceConditions, c *state.ObjectUploadCompletion, dispatched *bool) (objectstorage.CopyObjectResult, error, bool) {
	var err error
	var result objectstorage.CopyObjectResult
	ctx, err = h.protectionContext(ctx, req, c.Protection)
	if err != nil {
		return result, err, true
	}
	if !c.Encryption.Empty() {
		ctx = h.encryptionContext(ctx, req)
		ctx = objectstorage.WithEncryptionWriteRecorder(ctx, func(ctx context.Context) error {
			if h.requestMetrics == nil {
				return objectstorage.ErrConfiguration
			}
			if err := h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC()); err != nil {
				return err
			}
			intent, err := st.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
			if err == nil {
				*c, *dispatched = intent, true
			}
			return err
		})
		if req.copySource != nil {
			result, err = req.provider.(objectstorage.CrossBucketTrackedObjectCopier).CopyCrossBucketTrackedObject(ctx, req.copySource.PhysicalName, req.bucket.PhysicalName, c.ID, copy, source, conditions, c.Encryption)
		} else {
			result, err = req.provider.(objectstorage.ObjectEncryptionProvider).CopyEncryptedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source, conditions, c.Encryption)
		}
	} else {
		intent, ok := h.dispatchGatewayPut(w, r, req, st, *c)
		if !ok {
			return objectstorage.CopyObjectResult{}, objectstorage.ErrUnavailable, false
		}
		*c, *dispatched = intent, true
		if req.copySource != nil {
			result, err = req.provider.(objectstorage.CrossBucketTrackedObjectCopier).CopyCrossBucketTrackedObject(ctx, req.copySource.PhysicalName, req.bucket.PhysicalName, c.ID, copy, source, conditions, c.Encryption)
		} else if conditions.HasDates() {
			result, err = copier.(objectstorage.DateConditionalTrackedObjectCopier).CopyDateConditionalTrackedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source, conditions)
		} else if conditions.Empty() {
			result, err = copier.CopyTrackedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source)
		} else {
			result, err = copier.(objectstorage.ConditionalTrackedObjectCopier).CopyConditionalTrackedObject(ctx, req.bucket.PhysicalName, c.ID, copy, source, conditions)
		}
	}
	return result, err, true
}
