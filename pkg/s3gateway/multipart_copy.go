package s3gateway

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) copyMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID, rawPart string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	c, req, ok := h.multipartCopyRequest(w, r, req, key, rawPart)
	if !ok {
		return
	}
	upload, _, ok := h.loadPublicMultipart(w, r, req, uploadID, key)
	if !ok {
		return
	}
	if upload.State != state.ObjectMultipartActive || !upload.ExpiresAt.After(h.now()) {
		h.writeMultipartError(w, r, req, state.ErrConflict, "NoSuchUpload")
		return
	}
	c.ProviderUploadID = upload.ProviderUploadID
	copier, capable := req.provider.(objectstorage.MultipartPartCopier)
	transfers, fenced := h.multipartStore.(state.ObjectMultipartTransferStore)
	if !capable || !fenced {
		h.unsupported(w, r, req.requestID)
		return
	}
	if c.Conditions.HasDates() {
		if _, ok := req.provider.(objectstorage.DateConditionalMultipartPartCopier); !ok {
			h.unsupported(w, r, req.requestID)
			return
		}
	}
	select {
	case h.putSlots <- struct{}{}:
		defer func() { <-h.putSlots }()
	default:
		writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "Please reduce your request rate.", r.URL.Path, req.requestID)
		return
	}
	h.forwardMultipartCopy(w, r, req, upload, c, copier, transfers)
}

func (h *Handler) multipartCopyRequest(w http.ResponseWriter, r *http.Request, req requestContext, key, rawPart string) (objectstorage.MultipartPartCopyRequest, requestContext, bool) {
	c := objectstorage.MultipartPartCopyRequest{Key: key}
	part, err := strconv.ParseInt(rawPart, 10, 32)
	if err != nil || part < 1 || part > api.MaxMultipartParts {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return c, req, false
	}
	if !h.validCopyBody(w, r, req) {
		return c, req, false
	}
	c.PartNumber = int32(part)
	source, err := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if err != nil {
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified copy source does not exist.", r.URL.Path, req.requestID)
		return c, req, false
	}
	req, ok := h.authorizeGatewayCopySource(w, r, req, source, true)
	if !ok {
		return c, req, false
	}
	c.SourceKey = source.Key
	c.SourceProviderVersionID, ok = h.resolveCopyVersion(w, r, copySourceContext(req), source, true)
	if !ok {
		return c, req, false
	}
	c.Range, err = objectstorage.ParseCopySourceRange(r.Header.Get("X-Amz-Copy-Source-Range"))
	values, present := r.Header[http.CanonicalHeaderKey("X-Amz-Copy-Source-Range")]
	if err != nil || present && (len(values) != 1 || values[0] == "") {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return c, req, false
	}
	// Part copies use metadata/tags from initiation, never per-part directives.
	for _, name := range []string{"X-Amz-Metadata-Directive", "X-Amz-Tagging-Directive", "X-Amz-Tagging"} {
		if r.Header.Get(name) != "" {
			h.unsupported(w, r, req.requestID)
			return c, req, false
		}
	}
	c.Conditions, ok = gatewayCopyConditions(w, r, req)
	return c, req, ok
}

func (h *Handler) forwardMultipartCopy(w http.ResponseWriter, r *http.Request, req requestContext, upload state.ObjectMultipartUpload, c objectstorage.MultipartPartCopyRequest, copier objectstorage.MultipartPartCopier, transfers state.ObjectMultipartTransferStore) {
	ctx, cancel := context.WithTimeout(r.Context(), h.transferTimeout)
	defer cancel()
	sourceReq := copySourceContext(req)
	if !h.admit(w, r, sourceReq, c.SourceKey, 0, false) || !h.recordProviderRequest(w, r, sourceReq) {
		return
	}
	source, err := snapshotGatewayPartCopySource(ctx, sourceReq, c, copier)
	if err != nil {
		h.providerError(w, r, req, err, c.SourceKey)
		return
	}
	sourceID, ok := h.publicObjectVersionID(w, r, sourceReq, c.SourceKey, source.ProviderVersionID, false)
	if !ok {
		return
	}
	if !h.checkCopySource(w, r, sourceReq, c.SourceKey, source, c.Conditions) {
		return
	}
	size, err := objectstorage.MultipartCopySize(source, c.Range)
	if err != nil || size > h.registry.MaxPartBytes {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	token := uuid.NewString()
	if req.copySource != nil {
		cross, ok := transfers.(state.ObjectCrossBucketMultipartStore)
		if !ok {
			h.unsupported(w, r, req.requestID)
			return
		}
		// Account for the request before atomically claiming its single dispatch.
		if !h.recordProviderRequest(w, r, req) {
			return
		}
		err = cross.BeginObjectCrossBucketMultipartPart(ctx, req.bucket.AccountID, req.bucket.ID, upload.ID, token, c.PartNumber, size, h.registry.MaxUploadBytes, h.registry.Accounting, state.ObjectMultipartCopySource{SubjectID: req.credential.ID, BucketID: req.copySource.ID, GrantID: req.copyGrantID, Key: c.SourceKey})
	} else {
		err = transfers.BeginObjectMultipartPart(ctx, req.bucket.AccountID, req.bucket.ID, upload.ID, token, c.PartNumber, size, h.registry.MaxUploadBytes, h.registry.Accounting)
	}
	if !h.writeMultipartAdmissionError(w, r, req, err) {
		return
	}
	safeToSettle := true
	defer func() {
		if safeToSettle {
			h.settleMultipartTransfer(ctx, req, upload.ID, c.PartNumber, token, transfers)
		}
	}()
	if req.copySource == nil && !h.recordProviderRequest(w, r, req) {
		return
	}
	receipt, err := objectstorageactivity.DispatchMultipartPartCopy(ctx, h.store, transfers, req.bucket, upload.ID, c.PartNumber, token, multipartPartCopyIntent(sourceReq.bucket, c, source, size))
	if err != nil {
		h.providerError(w, r, req, err, upload.Key)
		return
	}
	safeToSettle = false
	ctx = objectstorage.WithMultipartCopyReadRecorder(ctx, func(ctx context.Context, bytes int64) error {
		if err := objectstorage.RecordGatewayProviderRequest(ctx, h.requestMetrics, sourceReq.bucket.ID, h.now().UTC(), h.registry.Accounting); err != nil {
			return err
		}
		if h.registry.Accounting.GatewaySafety() {
			metrics, ok := h.requestMetrics.(state.ObjectStorageGatewayEgressStore)
			if !ok {
				return objectstorage.ErrConfiguration
			}
			return metrics.ReserveObjectStorageGatewayEgress(ctx, sourceReq.bucket.ID, bytes, h.now().UTC(), h.registry.Accounting)
		}
		return nil
	})
	var result objectstorage.CopyObjectResult
	if req.copySource != nil {
		result, err = req.provider.(objectstorage.CrossBucketMultipartPartCopier).CopyCrossBucketMultipartPart(ctx, req.copySource.PhysicalName, req.bucket.PhysicalName, c, source)
	} else if c.Conditions.HasDates() {
		result, err = copier.(objectstorage.DateConditionalMultipartPartCopier).CopyDateConditionalMultipartPart(ctx, req.bucket.PhysicalName, c, source)
	} else {
		result, err = copier.CopyMultipartPart(ctx, req.bucket.PhysicalName, c, source)
	}
	if err != nil {
		safeToSettle = errors.Is(err, objectstorage.ErrWriteRejected)
		if safeToSettle {
			safeToSettle = receipt.MultipartPartWriterID == ""
			if finishErr := objectstorageactivity.FinishMultipartPart(ctx, h.store, transfers, receipt); finishErr != nil {
				h.providerError(w, r, req, objectstorage.ErrUnavailable, upload.Key)
				return
			}
		}
		if !errors.Is(err, objectstorage.ErrWriteRejected) {
			h.providerError(w, r, req, objectstorage.ErrUnavailable, upload.Key)
		} else if errors.Is(err, objectstorage.ErrPreconditionFailed) {
			h.providerHTTPError(w, r, req, http.StatusPreconditionFailed, c.SourceKey)
		} else {
			h.providerError(w, r, req, err, c.SourceKey)
		}
		return
	}
	if !validGatewayETag(result.ETag) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, upload.Key)
		return
	}
	if err := objectstorageactivity.FinishMultipartPart(ctx, h.store, transfers, receipt); err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, upload.Key)
		return
	}
	safeToSettle = receipt.MultipartPartWriterID == ""
	if sourceID != "" {
		w.Header().Set("X-Amz-Copy-Source-Version-Id", sourceID)
	}
	lastModified := ""
	if !result.LastModified.IsZero() {
		lastModified = result.LastModified.UTC().Format(time.RFC3339Nano)
	}
	writeS3XML(w, http.StatusOK, req.requestID, copyMultipartPartResult{XMLNS: s3XMLNamespace, ETag: result.ETag, LastModified: lastModified})
}

type copyMultipartPartResult struct {
	XMLName      xml.Name `xml:"CopyPartResult"`
	XMLNS        string   `xml:"xmlns,attr"`
	ETag         string   `xml:"ETag"`
	LastModified string   `xml:"LastModified,omitempty"`
}

func multipartPartCopyIntent(b state.ObjectBucket, c objectstorage.MultipartPartCopyRequest, s objectstorage.CopySourceSnapshot, size int64) state.ObjectMultipartPartCopyIntent {
	i := state.ObjectMultipartPartCopyIntent{Schema: 1, SourceBucketID: b.ID, SourceBackendID: b.BackendID, SourceBackendFingerprint: b.BackendFingerprint, SourcePhysicalName: b.PhysicalName, SourceKey: c.SourceKey, SourceVersionID: s.ProviderVersionID, SourceRequestedVersionID: c.SourceProviderVersionID, SourceETag: s.ETag, SourceSize: s.SizeBytes, DestinationKey: c.Key, ProviderUploadID: c.ProviderUploadID, ExpectedSize: size, IfMatch: c.Conditions.IfMatch, IfNoneMatch: c.Conditions.IfNoneMatch}
	if c.Range != nil {
		i.HasRange = true
		i.RangeFirst = c.Range.First
		i.RangeLast = c.Range.Last
	}
	if c.Conditions.IfModifiedSince != nil {
		i.IfModifiedSince = c.Conditions.IfModifiedSince.UTC().Format(time.RFC3339Nano)
	}
	if c.Conditions.IfUnmodifiedSince != nil {
		i.IfUnmodifiedSince = c.Conditions.IfUnmodifiedSince.UTC().Format(time.RFC3339Nano)
	}
	return i
}
