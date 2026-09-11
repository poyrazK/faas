package s3gateway

import (
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

func (h *Handler) loadPublicMultipart(w http.ResponseWriter, r *http.Request, req requestContext, uploadID string) (state.ObjectMultipartUpload, state.ObjectMultipartUploadStore, bool) {
	store, ok := h.publicMultipartStore(w, r, req)
	if !ok {
		return state.ObjectMultipartUpload{}, nil, false
	}
	if _, err := uuid.Parse(uploadID); err != nil {
		h.writeMultipartError(w, r, req, objectstorage.ErrNotFound, "NoSuchUpload")
		return state.ObjectMultipartUpload{}, nil, false
	}
	upload, err := store.GetObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, uploadID)
	if err != nil || upload.PartCount != 0 {
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
	store, ok := h.publicMultipartStore(w, r, req)
	if !ok {
		return
	}
	contentType := r.Header.Get("Content-Type")
	if objectstorage.ValidateContentType(contentType) != nil {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	upload, err := store.ReserveObjectMultipartUpload(r.Context(), state.ObjectMultipartUpload{
		ID: uuid.NewString(), AccountID: req.credential.AccountID, AppID: req.bucket.AppID, BucketID: req.bucket.ID,
		Key: key, ContentType: contentType, ExpiresAt: h.now().UTC().Add(publicMultipartTTL),
	}, api.MaxActiveMultipartUploadsPerBucket)
	if err != nil {
		h.writeMultipartError(w, r, req, err, "InvalidRequest")
		return
	}
	if upload.State == state.ObjectMultipartInitiating {
		token := uuid.NewString()
		claimed, claimErr := store.ClaimObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, upload.ID, token, state.ObjectMultipartInitiating, nil, false)
		if claimErr != nil {
			h.writeMultipartError(w, r, req, claimErr, "OperationAborted")
			return
		}
		if !h.recordProviderRequest(w, r, req) {
			return
		}
		providerID, providerErr := req.provider.EnsureMultipartUpload(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartCreateRequest{
			SessionID: claimed.ID, Key: claimed.Key, SizeBytes: 0, ContentType: claimed.ContentType,
		})
		if providerErr != nil {
			h.providerError(w, r, req, providerErr, key)
			return
		}
		if err = store.ActivateObjectMultipartUpload(r.Context(), claimed.ID, token, providerID); err != nil {
			h.writeMultipartError(w, r, req, err, "OperationAborted")
			return
		}
		upload, err = store.GetObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, claimed.ID)
		if err != nil {
			h.writeMultipartError(w, r, req, err, "NoSuchUpload")
			return
		}
	}
	writeS3XML(w, http.StatusOK, req.requestID, initiateMultipartResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: key, UploadID: upload.ID})
}

func (h *Handler) listMultipartUploads(w http.ResponseWriter, r *http.Request, req requestContext) {
	if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
		return
	}
	query := operationQuery(r.URL.Query())
	if !queryKeysOnly(query, "uploads", "prefix", "max-uploads") || !query.Has("uploads") {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	limit := int64(1000)
	if raw := query.Get("max-uploads"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > 1000 {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
			return
		}
		limit = parsed
	}
	store, ok := h.publicMultipartStore(w, r, req)
	if !ok {
		return
	}
	prefix := query.Get("prefix")
	items := make([]listedMultipartUpload, 0, limit)
	cursor := ""
	truncated := false
	for page := 0; page < 20 && int64(len(items)) < limit; page++ {
		pageLimit := int32(min(int64(100), limit-int64(len(items))))
		rows, next, err := store.ListObjectMultipartUploads(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, pageLimit, cursor)
		if err != nil {
			h.writeMultipartError(w, r, req, err, "ServiceUnavailable")
			return
		}
		for _, upload := range rows {
			if upload.PartCount == 0 && (upload.State == state.ObjectMultipartActive || upload.State == state.ObjectMultipartCompleting || upload.State == state.ObjectMultipartAborting) && strings.HasPrefix(upload.Key, prefix) {
				items = append(items, listedMultipartUpload{Key: upload.Key, UploadID: upload.ID, Initiated: upload.CreatedAt.UTC().Format(time.RFC3339Nano)})
				if int64(len(items)) == limit {
					break
				}
			}
		}
		if next == "" {
			cursor = ""
			break
		}
		cursor, truncated = next, true
	}
	writeS3XML(w, http.StatusOK, req.requestID, listMultipartUploadsResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Prefix: prefix, MaxUploads: int32(limit), IsTruncated: truncated, NextUploadMarker: cursor, Uploads: items})
}

func (h *Handler) uploadMultipartPart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID, rawPart string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, _, ok := h.loadPublicMultipart(w, r, req, uploadID)
	if !ok {
		return
	}
	if upload.State != state.ObjectMultipartActive || !upload.ExpiresAt.After(h.now()) {
		h.writeMultipartError(w, r, req, state.ErrConflict, "NoSuchUpload")
		return
	}
	part64, err := strconv.ParseInt(rawPart, 10, 32)
	if err != nil || part64 < 1 || part64 > api.MaxMultipartParts || r.ContentLength < 1 || r.ContentLength > min(h.registry.MaxUploadBytes, api.MaxObjectSinglePutBytes) {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	if !h.admit(w, r, req, key, 0, false) {
		return
	}
	if req.signature.PayloadHash != "UNSIGNED-PAYLOAD" {
		writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "Multipart uploads with signed streaming payloads are not implemented by Gregale yet.", r.URL.Path, req.requestID)
		return
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	signed, err := req.provider.PresignMultipartPart(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartPartRequest{
		Key: key, ProviderUploadID: upload.ProviderUploadID, PartNumber: int32(part64), SizeBytes: r.ContentLength, ExpiresIn: 60,
	})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPut, signed.URL, io.LimitReader(r.Body, r.ContentLength))
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	upstream.ContentLength = r.ContentLength
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
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

func (h *Handler) listMultipartParts(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID string, query url.Values) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, _, ok := h.loadPublicMultipart(w, r, req, uploadID)
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
	page, err := req.provider.ListMultipartParts(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartListPartsRequest{Key: key, ProviderUploadID: upload.ProviderUploadID, PartNumberMarker: marker, Limit: limit})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	items := make([]listedMultipartPart, 0, len(page.Items))
	for _, part := range page.Items {
		items = append(items, listedMultipartPart{PartNumber: part.PartNumber, ETag: part.ETag, Size: part.SizeBytes, LastModified: part.LastModified.UTC().Format(time.RFC3339Nano)})
	}
	writeS3XML(w, http.StatusOK, req.requestID, listMultipartPartsResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: key, UploadID: upload.ID, PartNumberMarker: marker, MaxParts: limit, IsTruncated: page.NextPartNumberMarker != 0, NextPartNumberMarker: page.NextPartNumberMarker, Parts: items})
}

func (h *Handler) completeMultipart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, store, ok := h.loadPublicMultipart(w, r, req, uploadID)
	if !ok {
		return
	}
	if upload.State == state.ObjectMultipartCompleted {
		writeS3XML(w, http.StatusOK, req.requestID, completeMultipartResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: key, ETag: multipartETag(upload.Parts), UploadID: upload.ID})
		return
	}
	if upload.State != state.ObjectMultipartActive && upload.State != state.ObjectMultipartCompleting {
		h.writeMultipartError(w, r, req, state.ErrConflict, "NoSuchUpload")
		return
	}
	var body completeMultipartUploadRequest
	decoder := xml.NewDecoder(io.LimitReader(r.Body, 4<<20))
	if err := decoder.Decode(&body); err != nil || len(body.Parts) == 0 || len(body.Parts) > api.MaxMultipartParts {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "MalformedXML")
		return
	}
	parts := make([]api.ObjectMultipartCompletedPart, len(body.Parts))
	var previousPart int32
	for i, part := range body.Parts {
		if part.PartNumber < 1 || part.PartNumber > api.MaxMultipartParts || part.PartNumber <= previousPart || len(part.ETag) == 0 || len(part.ETag) > 256 || !objectstorage.ValidKey(strings.Trim(part.ETag, `"`)) {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidPart")
			return
		}
		previousPart = part.PartNumber
		parts[i] = api.ObjectMultipartCompletedPart{PartNumber: part.PartNumber, ETag: part.ETag}
	}
	sizes, err := h.multipartPartSizes(r, req, key, upload)
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	var total int64
	for _, part := range parts {
		providerPart, found := sizes[part.PartNumber]
		if !found || strings.Trim(providerPart.ETag, `"`) != strings.Trim(part.ETag, `"`) {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidPart")
			return
		}
		if providerPart.SizeBytes < 1 || total > api.MaxObjectUploadBytes-providerPart.SizeBytes {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "EntityTooLarge")
			return
		}
		if len(parts) > 1 && part.PartNumber != parts[len(parts)-1].PartNumber && providerPart.SizeBytes < api.MinMultipartPartBytes {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "EntityTooSmall")
			return
		}
		total += providerPart.SizeBytes
	}
	if total < 1 || total > h.registry.MaxUploadBytes {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "EntityTooLarge")
		return
	}
	if !h.admit(w, r, req, key, total, true) {
		return
	}
	token := uuid.NewString()
	claimed, err := store.ClaimObjectMultipartUpload(r.Context(), req.credential.AccountID, req.bucket.AppID, req.bucket.ID, upload.ID, token, state.ObjectMultipartCompleting, parts, false)
	if err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	if err = store.SetObjectMultipartUploadSize(r.Context(), claimed.ID, token, total); err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	claimed.SizeBytes = total
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	if err = req.provider.CompleteMultipartUpload(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartCompleteRequest{SessionID: claimed.ID, Key: key, ProviderUploadID: claimed.ProviderUploadID, SizeBytes: total, Parts: toProviderParts(parts)}); err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	if err = store.FinishObjectMultipartUpload(r.Context(), claimed.ID, token, state.ObjectMultipartCompleted); err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	writeS3XML(w, http.StatusOK, req.requestID, completeMultipartResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: key, ETag: multipartETag(parts), UploadID: upload.ID})
}

func (h *Handler) abortMultipart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, store, ok := h.loadPublicMultipart(w, r, req, uploadID)
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
	if err = req.provider.AbortMultipartUpload(r.Context(), req.bucket.PhysicalName, objectstorage.MultipartAbortRequest{Key: key, ProviderUploadID: claimed.ProviderUploadID}); err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	if err = store.FinishObjectMultipartUpload(r.Context(), claimed.ID, token, state.ObjectMultipartAborted); err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
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
