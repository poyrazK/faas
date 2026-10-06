package s3gateway

import (
	"context"
	"crypto/md5" // #nosec G501 -- multipart ETag compatibility uses the S3 MD5 convention.
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

const publicMultipartTTL = 24 * time.Hour

type completeMultipartUploadRequest struct {
	XMLName xml.Name                `xml:"CompleteMultipartUpload"`
	Parts   []completeMultipartPart `xml:"Part"`
}

type completeMultipartPart struct {
	PartNumber int32  `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (h *Handler) publicMultipartStore(w http.ResponseWriter, r *http.Request, req requestContext) (state.ObjectMultipartUploadStore, bool) {
	if h.multipartStore == nil {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale multipart storage is unavailable.", r.URL.Path, req.requestID)
		return nil, false
	}
	return h.multipartStore, true
}

func (h *Handler) loadPublicMultipart(w http.ResponseWriter, r *http.Request, req requestContext, uploadID, key string) (state.ObjectMultipartUpload, state.ObjectMultipartUploadStore, bool) {
	store, ok := h.publicMultipartStore(w, r, req)
	if !ok {
		return state.ObjectMultipartUpload{}, nil, false
	}
	if _, err := uuid.Parse(uploadID); err != nil {
		h.writeMultipartError(w, r, req, objectstorage.ErrNotFound, "NoSuchUpload")
		return state.ObjectMultipartUpload{}, nil, false
	}
	upload, err := store.GetObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, uploadID)
	fixedURL := req.credential.URL != nil && req.credential.URL.Multipart != nil
	if err != nil || upload.PartCount != 0 && !fixedURL || fixedURL && upload.PartCount == 0 || upload.Key != key {
		if err == nil {
			err = objectstorage.ErrNotFound
		}
		h.writeMultipartError(w, r, req, err, "NoSuchUpload")
		return state.ObjectMultipartUpload{}, nil, false
	}
	return upload, store, true
}

func (h *Handler) initiateMultipart(w http.ResponseWriter, r *http.Request, req requestContext) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	key := req.signatureKey(r)
	if !objectstorage.ValidKey(key) {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidRequest")
		return
	}
	// A non-empty body is permitted by S3, but ignoring it would make the
	// signed request ambiguous, so reject it explicitly.
	if r.ContentLength != 0 {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidRequest")
		return
	}
	if !h.admit(w, r, req, req.signatureKey(r), 0, false) {
		return
	}
	upload, store, ok := h.admitPublicMultipart(w, r, req, key)
	if !ok {
		return
	}
	if upload.State == state.ObjectMultipartInitiating {
		upload, ok = h.activatePublicMultipart(w, r, req, store, upload)
		if !ok {
			return
		}
	}
	if upload.State != state.ObjectMultipartActive || !upload.ExpiresAt.After(h.now()) {
		h.writeMultipartError(w, r, req, state.ErrConflict, "OperationAborted")
		return
	}
	writeEncryptionHeaders(w.Header(), upload.Encryption.Selection)
	writeS3XML(w, http.StatusOK, req.requestID, initiateMultipartResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: key, UploadID: upload.ID})
}

func (h *Handler) uploadMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID, rawPart string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
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
	part64, err := strconv.ParseInt(rawPart, 10, 32)
	if err != nil || part64 < 1 || part64 > api.MaxMultipartParts || r.ContentLength < 1 || r.ContentLength > h.registry.MaxPartBytes {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	integrity, err := newRequestIntegrityReader(r.Body, r.ContentLength, req.signature.PayloadHash, r.Header)
	if err != nil {
		h.writeAWSChunkedError(w, r, req.requestID, err)
		return
	}
	transfers, ok := h.multipartStore.(state.ObjectMultipartTransferStore)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return
	}
	select {
	case h.putSlots <- struct{}{}:
		defer func() { <-h.putSlots }()
	default:
		closeUnreadUploadConnection(w, r)
		writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "Please reduce your request rate.", r.URL.Path, req.requestID)
		return
	}
	h.forwardMultipartPart(w, r, req, upload, int32(part64), integrity, transfers)
}

func (h *Handler) forwardMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, upload state.ObjectMultipartUpload, part int32, integrity *requestIntegrityReader, transfers state.ObjectMultipartTransferStore) {
	key := upload.Key
	timeout := h.transferTimeout
	if req.credential.URL != nil {
		timeout = min(timeout, api.ObjectTransferTimeout)
	}
	transferCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	transferToken := uuid.NewString()
	if !h.beginMultipartPart(w, r, req, transferCtx, upload, transferToken, part, transfers) {
		return
	}

	safeToSettle := true // No provider write has started yet.
	defer func() {
		if safeToSettle {
			h.settleMultipartTransfer(transferCtx, req, upload.ID, part, transferToken, transfers)
		}
	}()
	body := io.Reader(integrity)
	if !upload.Protection.Empty() {
		file, checksum, cleanup, ok := h.stageProtectedMultipartPart(w, r, req, integrity)
		if !ok {
			return
		}
		defer cleanup()
		body = file
		var bindErr error
		transferCtx, bindErr = h.protectionContext(transferCtx, req, upload.Protection)
		if bindErr != nil {
			h.providerError(w, r, req, bindErr, key)
			return
		}
		transferCtx = objectstorage.WithObjectWriteChecksum(transferCtx, checksum)
	}
	if req.credential.URL == nil && !h.recordProviderRequest(w, r, req) {
		return
	}
	signed, err := req.provider.PresignMultipartPart(transferCtx, req.bucket.PhysicalName, objectstorage.MultipartPartRequest{
		Key: upload.Key, ProviderUploadID: upload.ProviderUploadID, PartNumber: part, SizeBytes: r.ContentLength, ExpiresIn: api.ObjectMultipartPartURLTTLSeconds,
	})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	upstream, err := http.NewRequestWithContext(transferCtx, http.MethodPut, signed.URL, io.LimitReader(body, r.ContentLength))
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	upstream.ContentLength = r.ContentLength
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	safeToSettle = false
	response, err := h.doMutationRequest(upstream, req)
	if err != nil {
		if integrity.err != nil && h.writeAWSChunkedError(w, r, req.requestID, integrity.err) {
			return
		}
		if req.streaming != nil && req.streaming.err != nil && h.writeAWSChunkedError(w, r, req.requestID, req.streaming.err) {
			return
		}
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	if integrity.err != nil && h.writeAWSChunkedError(w, r, req.requestID, integrity.err) {
		return
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && integrity.remaining != 0 {
		writeS3Error(w, http.StatusBadRequest, "IncompleteBody", "You did not provide the number of bytes specified by Content-Length.", r.URL.Path, req.requestID)
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		h.providerHTTPError(w, r, req, response.StatusCode, key)
		return
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	if !h.multipartPartEncryption(w, r, req, upload, response.Header) {
		return
	}
	safeToSettle = true
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) beginMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, ctx context.Context, upload state.ObjectMultipartUpload, token string, part int32, transfers state.ObjectMultipartTransferStore) bool {
	var err error
	if req.credential.URL != nil && req.credential.URL.Multipart != nil {
		st, ok := h.store.(state.ObjectMultipartURLCapabilityStore)
		if !ok {
			h.unsupported(w, r, req.requestID)
			return false
		}
		err = st.BeginObjectURLMultipartPart(ctx, req.credential.ID, token, h.registry.Accounting)
	} else {
		err = transfers.BeginObjectMultipartPart(ctx, req.bucket.AccountID, req.bucket.ID, upload.ID, token, part, r.ContentLength, h.registry.MaxUploadBytes, h.registry.Accounting)
	}
	return h.writeMultipartAdmissionError(w, r, req, err)
}

func (h *Handler) listMultipartParts(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID string, query url.Values) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, _, ok := h.loadPublicMultipart(w, r, req, uploadID, key)
	if !ok {
		return
	}
	marker, limit, err := multipartListOptions(query)
	if err != nil {
		h.writeMultipartError(w, r, req, err, "InvalidArgument")
		return
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	page, err := req.provider.ListMultipartParts(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartListPartsRequest{Key: upload.Key, ProviderUploadID: upload.ProviderUploadID, PartNumberMarker: marker, Limit: limit})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	items := make([]listedMultipartPart, 0, len(page.Items))
	for _, part := range page.Items {
		items = append(items, listedMultipartPart{PartNumber: part.PartNumber, ETag: part.ETag, Size: part.SizeBytes, LastModified: part.LastModified.UTC().Format(time.RFC3339Nano)})
	}
	writeS3XML(w, http.StatusOK, req.requestID, listMultipartPartsResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: upload.Key, UploadID: upload.ID, PartNumberMarker: marker, MaxParts: limit, IsTruncated: page.NextPartNumberMarker != 0, NextPartNumberMarker: page.NextPartNumberMarker, Parts: items})
}

func (h *Handler) abortMultipart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, store, ok := h.loadPublicMultipart(w, r, req, uploadID, key)
	if !ok {
		return
	}
	if upload.State == state.ObjectMultipartAborted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if upload.State != state.ObjectMultipartActive && upload.State != state.ObjectMultipartAborting {
		h.writeMultipartError(w, r, req, state.ErrConflict, "InvalidRequest")
		return
	}
	token := uuid.NewString()
	claimed, err := store.ClaimObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, upload.ID, token, state.ObjectMultipartAborting, nil, false)
	if err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	if err = h.mutate(r.Context(), req, func(mutationCtx context.Context) error {
		return req.provider.AbortMultipartUpload(mutationCtx, req.bucket.PhysicalName, objectstorage.MultipartAbortRequest{Key: claimed.Key, ProviderUploadID: claimed.ProviderUploadID})
	}); err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	if !h.finishMultipartAbort(w, r, req, store, claimed) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) multipartPartSizes(r *http.Request, req requestContext, key string, upload state.ObjectMultipartUpload) (map[int32]objectstorage.MultipartPart, error) {
	parts := map[int32]objectstorage.MultipartPart{}
	marker := int32(0)
	for page := 0; page < 20; page++ {
		if h.requestMetrics != nil {
			if err := h.requestMetrics.RecordObjectStorageProviderRequest(r.Context(), req.bucket.ID, h.now().UTC()); err != nil {
				return nil, objectstorage.ErrUnavailable
			}
		}
		out, err := req.provider.ListMultipartParts(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartListPartsRequest{Key: key, ProviderUploadID: upload.ProviderUploadID, PartNumberMarker: marker, Limit: 1000})
		if err != nil {
			return nil, err
		}
		for _, part := range out.Items {
			if part.PartNumber < 1 || part.PartNumber > api.MaxMultipartParts || part.SizeBytes < 1 || len(part.ETag) == 0 || len(part.ETag) > 256 || !objectstorage.ValidKey(strings.Trim(part.ETag, `"`)) {
				return nil, objectstorage.ErrUnavailable
			}
			if _, exists := parts[part.PartNumber]; exists {
				return nil, objectstorage.ErrUnavailable
			}
			parts[part.PartNumber] = part
		}
		if out.NextPartNumberMarker == 0 {
			return parts, nil
		}
		if out.NextPartNumberMarker <= marker {
			return nil, objectstorage.ErrUnavailable
		}
		marker = out.NextPartNumberMarker
	}
	return nil, objectstorage.ErrUnavailable
}

func multipartListOptions(query url.Values) (int32, int32, error) {
	marker, limit := int64(0), int64(1000)
	if raw := query.Get("part-number-marker"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, 0, objectstorage.ErrInvalid
		}
		marker = parsed
	}
	if raw := query.Get("max-parts"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return 0, 0, objectstorage.ErrInvalid
		}
		limit = parsed
	}
	if marker < 0 || marker > api.MaxMultipartParts || limit < 1 || limit > 1000 {
		return 0, 0, objectstorage.ErrInvalid
	}
	return int32(marker), int32(limit), nil
}

func toProviderParts(parts []api.ObjectMultipartCompletedPart) []objectstorage.CompletedPart {
	out := make([]objectstorage.CompletedPart, len(parts))
	for i, part := range parts {
		out[i] = objectstorage.CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag}
	}
	return out
}

func multipartETag(parts []api.ObjectMultipartCompletedPart) string {
	digest := md5.New() // #nosec G401 -- S3 multipart ETags use the MD5-of-part-digests convention.
	for _, part := range parts {
		raw, err := hex.DecodeString(strings.Trim(part.ETag, `"`))
		if err != nil || len(raw) != md5.Size {
			return ""
		}
		// codeql[go/weak-sensitive-data-hashing] -- S3 multipart ETags require this MD5-of-part-digests identifier; it is not a security hash.
		_, _ = digest.Write(raw)
	}
	if len(parts) == 0 {
		return ""
	}
	return `"` + hex.EncodeToString(digest.Sum(nil)) + `-` + strconv.Itoa(len(parts)) + `"`
}

func (h *Handler) writeMultipartError(w http.ResponseWriter, r *http.Request, req requestContext, err error, fallback string) {
	if errors.Is(err, objectstorage.ErrNotFound) || errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, http.StatusNotFound, fallback, "The specified multipart upload does not exist.", r.URL.Path, req.requestID)
		return
	}
	if errors.Is(err, state.ErrConflict) || errors.Is(err, objectstorage.ErrConflict) {
		writeS3Error(w, http.StatusConflict, fallback, "The multipart upload is not in a valid state for this operation.", r.URL.Path, req.requestID)
		return
	}
	if errors.Is(err, objectstorage.ErrInvalid) {
		status := http.StatusBadRequest
		if fallback == "EntityTooLarge" {
			status = http.StatusBadRequest
		}
		writeS3Error(w, status, fallback, "The multipart request is invalid.", r.URL.Path, req.requestID)
		return
	}
	writeS3Error(w, http.StatusServiceUnavailable, fallback, "Gregale could not complete the multipart operation.", r.URL.Path, req.requestID)
}

// signatureKey is populated by routeObject before dispatch. Keeping it on the
// request context would make the auth path harder to audit, so the key is
// recovered from the already validated path instead.
func (req requestContext) signatureKey(r *http.Request) string {
	_, key, _, hasKey, _ := parsePath(r.URL.EscapedPath())
	if !hasKey {
		return ""
	}
	return key
}

// Abort is accepted once provider writes are fenced. Pending cleanup stays in
// aborting state for the durable recovery worker, without releasing capacity.
func (h *Handler) finishMultipartAbort(w http.ResponseWriter, r *http.Request, req requestContext, store state.ObjectMultipartUploadStore, u state.ObjectMultipartUpload) bool {
	transfers, ok := store.(state.ObjectMultipartTransferStore)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return false
	}
	ready, err := transfers.ObjectMultipartAbortReady(r.Context(), u.ID, u.LeaseToken)
	if err == nil && ready {
		if !h.recordProviderRequest(w, r, req) {
			return false
		}
		err = objectstorage.VerifyMultipartAbort(r.Context(), req.provider, req.bucket.PhysicalName, objectstorage.MultipartAbortRequest{Key: u.Key, ProviderUploadID: u.ProviderUploadID})
		if err == nil {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			err = transfers.FinishVerifiedObjectMultipartAbort(finishCtx, u.ID, u.LeaseToken)
		}
	}
	if err == nil && ready {
		return true
	}
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if retryErr := store.RetryObjectMultipartUpload(retryCtx, u.ID, u.LeaseToken, "cleanup_pending", api.ObjectMultipartCleanupRetry); retryErr != nil {
		err = retryErr
	}
	if err != nil && !errors.Is(err, objectstorage.ErrConflict) {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return false
	}
	return true
}

func (h *Handler) writeMultipartAdmissionError(w http.ResponseWriter, r *http.Request, req requestContext, err error) bool {
	if errors.Is(err, state.ErrConflict) {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return false
	}
	return h.writeAdmissionError(w, r, req, err)
}

func (h *Handler) settleMultipartTransfer(parent context.Context, req requestContext, uploadID string, part int32, token string, transfers state.ObjectMultipartTransferStore) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	if err := transfers.SettleObjectMultipartPart(ctx, req.bucket.AccountID, uploadID, part, token); err != nil {
		h.log.Warn("S3 multipart transfer settlement deferred", "request_id", req.requestID)
	}
}

func (h *Handler) admitPublicMultipart(w http.ResponseWriter, r *http.Request, req requestContext, key string) (state.ObjectMultipartUpload, state.ObjectMultipartUploadStore, bool) {
	store, ok := h.publicMultipartStore(w, r, req)
	if !ok {
		return state.ObjectMultipartUpload{}, nil, false
	}
	metadata, metadataErr := objectMetadataFromHeaders(r)
	if metadataErr != nil {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return state.ObjectMultipartUpload{}, nil, false
	}
	upload, err := store.ReserveObjectMultipartUpload(r.Context(), state.ObjectMultipartUpload{
		ID: uuid.NewString(), AccountID: req.credential.AccountID, AppID: req.bucket.AppID, BucketID: req.bucket.ID,
		Key: key, ContentType: metadata.ContentType, Protection: req.protection.Clone(), Encryption: req.encryption.Clone(), ExpiresAt: h.now().UTC().Add(publicMultipartTTL),
		Metadata: state.ObjectMultipartMetadata{
			CacheControl: metadata.CacheControl, ContentDisposition: metadata.ContentDisposition,
			ContentEncoding: metadata.ContentEncoding, ContentLanguage: metadata.ContentLanguage,
			UserMetadata: metadata.Metadata, Tags: metadata.Tags,
		},
	}, api.MaxActiveMultipartUploadsPerBucket)
	if err != nil {
		h.writeMultipartError(w, r, req, err, "InvalidRequest")
		return state.ObjectMultipartUpload{}, nil, false
	}
	return upload, store, true
}

func (h *Handler) activatePublicMultipart(w http.ResponseWriter, r *http.Request, req requestContext, store state.ObjectMultipartUploadStore, upload state.ObjectMultipartUpload) (state.ObjectMultipartUpload, bool) {
	token := uuid.NewString()
	claimed, claimErr := store.ClaimObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, upload.ID, token, state.ObjectMultipartInitiating, nil, false)
	if claimErr != nil {
		h.writeMultipartError(w, r, req, claimErr, "OperationAborted")
		return state.ObjectMultipartUpload{}, false
	}
	providerID, providerErr := objectstorageactivity.Execute(r.Context(), h.store, req.bucket, func(mutationCtx context.Context) (string, error) {
		return h.ensureCapturedMultipart(mutationCtx, req, claimed)
	})
	if providerErr != nil {
		h.providerError(w, r, req, providerErr, upload.Key)
		return state.ObjectMultipartUpload{}, false
	}
	if err := store.ActivateObjectMultipartUpload(r.Context(), claimed.ID, token, providerID); err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return state.ObjectMultipartUpload{}, false
	}
	upload, err := store.GetObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, claimed.ID)
	if err != nil {
		h.writeMultipartError(w, r, req, err, "NoSuchUpload")
		return state.ObjectMultipartUpload{}, false
	}
	return upload, true
}

func (h *Handler) proxyMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, upload state.ObjectMultipartUpload, part int32) {
	key := upload.Key
	if r.ContentLength < 1 || r.ContentLength > min(h.registry.MaxUploadBytes, api.MaxObjectSinglePutBytes) {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	if !h.admit(w, r, req, key, 0, false) {
		return
	}
	integrity, err := newRequestIntegrityReader(r.Body, r.ContentLength, req.signature.PayloadHash, r.Header)
	if err != nil {
		h.writeAWSChunkedError(w, r, req.requestID, err)
		return
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	signed, err := req.provider.PresignMultipartPart(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartPartRequest{
		Key: upload.Key, ProviderUploadID: upload.ProviderUploadID, PartNumber: part, SizeBytes: r.ContentLength, ExpiresIn: 60,
	})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPut, signed.URL, io.LimitReader(integrity, r.ContentLength))
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	upstream.ContentLength = r.ContentLength
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	response, err := h.doMutationRequest(upstream, req)
	if err != nil {
		if integrity.err != nil && h.writeAWSChunkedError(w, r, req.requestID, integrity.err) {
			return
		}
		if req.streaming != nil && req.streaming.err != nil && h.writeAWSChunkedError(w, r, req.requestID, req.streaming.err) {
			return
		}
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	if integrity.err != nil && h.writeAWSChunkedError(w, r, req.requestID, integrity.err) {
		return
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && integrity.remaining != 0 {
		writeS3Error(w, http.StatusBadRequest, "IncompleteBody", "You did not provide the number of bytes specified by Content-Length.", r.URL.Path, req.requestID)
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		h.providerHTTPError(w, r, req, response.StatusCode, key)
		return
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}
