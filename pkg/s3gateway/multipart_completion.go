package s3gateway

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) completeMultipart(w http.ResponseWriter, r *http.Request, req requestContext, key, uploadID string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	upload, store, ok := h.loadPublicMultipart(w, r, req, uploadID, key)
	if !ok {
		return
	}
	parts, ok := h.readMultipartCompletion(w, r, req)
	if !ok || h.replayMultipartCompletion(w, r, req, upload, parts) {
		return
	}
	transfers, ok := store.(state.ObjectMultipartTransferStore)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return
	}
	token := uuid.NewString()
	var claimed state.ObjectMultipartUpload
	var err error
	if state.ObjectMultipartIsCompleting(upload.State) {
		// The provider upload may already be gone after a lost response. Replay
		// the persisted intent without listing parts or admitting capacity again.
		claimed, err = store.ClaimObjectMultipartUpload(r.Context(), upload.AccountID, upload.AppID, upload.BucketID, upload.ID, token, upload.State, parts, false)
	} else if upload.State == state.ObjectMultipartActive {
		total, valid := h.multipartCompletionSize(w, r, req, upload, parts)
		if !valid {
			return
		}
		upload.CompletionConditions = writeConditions(r)
		claimed, err = transfers.PrepareObjectMultipartCompletion(r.Context(), upload, token, total, parts, h.registry.Accounting)
	} else {
		h.writeMultipartError(w, r, req, state.ErrConflict, "NoSuchUpload")
		return
	}
	if !h.writeMultipartAdmissionError(w, r, req, err) {
		return
	}
	h.executeMultipartCompletion(w, r, req, store, transfers, claimed)
}

func (h *Handler) readMultipartCompletion(w http.ResponseWriter, r *http.Request, req requestContext) ([]api.ObjectMultipartCompletedPart, bool) {
	bodyBytes, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, maxCompleteMultipartBodyBytes)
	if err != nil {
		if !h.writeAWSChunkedError(w, r, req.requestID, err) {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "MalformedXML")
		}
		return nil, false
	}
	var body completeMultipartUploadRequest
	decoder := xml.NewDecoder(bytes.NewReader(bodyBytes)) // #nosec G709 -- Size-bounded S3 XML into a fixed struct; encoding/xml does not resolve external entities.
	decoder.Strict = true
	if err := decoder.Decode(&body); err != nil || body.XMLName.Local != "CompleteMultipartUpload" || len(body.Parts) == 0 || len(body.Parts) > api.MaxMultipartParts {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "MalformedXML")
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "MalformedXML")
		return nil, false
	}
	parts := make([]api.ObjectMultipartCompletedPart, len(body.Parts))
	var previous int32
	for i, part := range body.Parts {
		if part.PartNumber < 1 || part.PartNumber > api.MaxMultipartParts || part.PartNumber <= previous || len(part.ETag) == 0 || len(part.ETag) > api.MaxObjectWriteETagBytes || !objectstorage.ValidKey(strings.Trim(part.ETag, `"`)) {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidPart")
			return nil, false
		}
		previous = part.PartNumber
		parts[i] = api.ObjectMultipartCompletedPart{PartNumber: part.PartNumber, ETag: part.ETag}
	}
	return parts, true
}

// Once an intent exists, every retry must carry exactly its parts and conditions.
func (h *Handler) replayMultipartCompletion(w http.ResponseWriter, r *http.Request, req requestContext, u state.ObjectMultipartUpload, parts []api.ObjectMultipartCompletedPart) bool {
	if !state.ObjectMultipartIsCompleting(u.State) && u.State != state.ObjectMultipartCompleted && u.CompletionErrorCode == "" {
		return false
	}
	if !slices.Equal(u.Parts, parts) {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidPart")
		return true
	}
	if u.CompletionConditions != writeConditions(r) {
		h.writeMultipartError(w, r, req, state.ErrConflict, "OperationAborted")
		return true
	}
	if h.writeMultipartCompletionFailure(w, r, req, u.CompletionErrorCode) {
		return true
	}
	if u.State == state.ObjectMultipartCompleted {
		writeMultipartCompleted(w, req, u)
		return true
	}
	return false
}

func (h *Handler) multipartCompletionSize(w http.ResponseWriter, r *http.Request, req requestContext, u state.ObjectMultipartUpload, parts []api.ObjectMultipartCompletedPart) (int64, bool) {
	sizes, err := h.multipartPartSizes(r, req, u.Key, u)
	if err != nil {
		h.providerError(w, r, req, err, u.Key)
		return 0, false
	}
	var total int64
	for i, part := range parts {
		providerPart, found := sizes[part.PartNumber]
		if !found || strings.Trim(providerPart.ETag, `"`) != strings.Trim(part.ETag, `"`) {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidPart")
			return 0, false
		}
		if providerPart.SizeBytes < 1 || total > api.MaxObjectUploadBytes-providerPart.SizeBytes {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "EntityTooLarge")
			return 0, false
		}
		if i < len(parts)-1 && providerPart.SizeBytes < api.MinMultipartPartBytes {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "EntityTooSmall")
			return 0, false
		}
		total += providerPart.SizeBytes
	}
	if total < 1 || total > h.registry.MaxUploadBytes {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "EntityTooLarge")
		return 0, false
	}
	return total, true
}

func (h *Handler) executeMultipartCompletion(w http.ResponseWriter, r *http.Request, req requestContext, store state.ObjectMultipartUploadStore, transfers state.ObjectMultipartTransferStore, u state.ObjectMultipartUpload) {
	if _, capable := req.provider.(objectstorage.MultipartResultCompleter); capable || !u.Encryption.Empty() {
		h.executeMultipartResult(w, r, req, store, u)
		return
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), api.ObjectMultipartOperationTimeout)
	defer cancel()
	err := objectstorageactivity.Run(callCtx, h.store, req.bucket, func(mutationCtx context.Context) error {
		return objectstorage.CompleteMultipart(mutationCtx, req.provider, req.bucket.PhysicalName, objectstorage.MultipartCompleteRequest{
			SessionID: u.ID, Key: u.Key, ProviderUploadID: u.ProviderUploadID, SizeBytes: u.SizeBytes, Parts: toProviderParts(u.Parts),
		}, u.CompletionConditions)
	})
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.ObjectUploadSettlementTimeout)
	defer finishCancel()
	if err != nil {
		code := objectstorage.MultipartCompletionFailureCode(err)
		var persistErr error
		if code != "" && u.State == state.ObjectMultipartCompletingConditional {
			persistErr = transfers.RejectObjectMultipartCompletion(finishCtx, u.ID, u.LeaseToken, code)
		} else {
			persistErr = store.RetryObjectMultipartUpload(finishCtx, u.ID, u.LeaseToken, "temporary", 30*time.Second)
		}
		if persistErr != nil {
			h.writeMultipartError(w, r, req, persistErr, "OperationAborted")
		} else if !h.writeMultipartCompletionFailure(w, r, req, code) {
			h.providerError(w, r, req, err, u.Key)
		}
		return
	}
	if err = store.FinishObjectMultipartUpload(finishCtx, u.ID, u.LeaseToken, state.ObjectMultipartCompleted); err != nil {
		h.writeMultipartError(w, r, req, err, "OperationAborted")
		return
	}
	writeMultipartCompleted(w, req, u)
}

func (h *Handler) writeMultipartCompletionFailure(w http.ResponseWriter, r *http.Request, req requestContext, code string) bool {
	switch code {
	case "precondition_failed":
		writeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed", "The object does not satisfy the write condition.", r.URL.Path, req.requestID)
	case "conditional_conflict":
		writeS3Error(w, http.StatusConflict, "ConditionalRequestConflict", "A conflicting write occurred. Start a new multipart upload and upload the parts again.", r.URL.Path, req.requestID)
	case "conditional_not_found":
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The object required by If-Match does not exist.", r.URL.Path, req.requestID)
	default:
		return false
	}
	return true
}

func writeMultipartCompleted(w http.ResponseWriter, req requestContext, u state.ObjectMultipartUpload) {
	etag := u.CompletionETag
	if etag == "" {
		etag = multipartETag(u.Parts)
	}
	if u.CompletionVersionID != "" {
		w.Header().Set("X-Amz-Version-Id", u.CompletionVersionID)
	}
	writeEncryptionHeaders(w.Header(), u.Encryption.Selection)
	writeS3XML(w, http.StatusOK, req.requestID, completeMultipartResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Key: u.Key, ETag: etag, UploadID: u.ID})
}
