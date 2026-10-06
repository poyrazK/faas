package s3gateway

import (
	"context"
	"crypto/md5" // #nosec G501 -- Required S3 Content-MD5 protocol checksum.
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
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
		if !dispatched && req.credential.URL == nil {
			c.Status = "failed"
			c.ErrorCode = "dispatch_failed"
			_, _ = h.finishGatewayPut(parent, st, c)
		}
	}(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), h.transferTimeout)
	defer cancel()
	upstream, err := h.gatewayPutRequest(ctx, r, req, key, file, metadata, c)
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
	response, err := objectstorageactivity.ExecuteUpload(ctx, h.store, req.bucket, c, func(context.Context) (*http.Response, error) {
		return client.Do(upstream)
	})
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
	ack, err := objectstorage.VerifyObjectWriteAcknowledgment(response.Header)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	c.ETag = ack.ETag
	verified, err := objectstorage.VerifyEncryptionAcknowledgment(response.Header, c.Encryption)
	if err != nil {
		h.providerError(w, r, req, err, c.Key)
		return
	}
	if !c.Protection.Empty() {
		ctx, bindErr := h.protectionContext(r.Context(), req, c.Protection)
		if bindErr != nil {
			h.providerError(w, r, req, bindErr, c.Key)
			return
		}
		c.VerifiedProtection, err = req.provider.(objectstorage.ObjectWriteProtectionProvider).ConfirmObjectWriteProtection(ctx, req.bucket.PhysicalName, c.Key, ack.ProviderVersionID, c.ID, c.Bytes, false, ack.ETag)
		if err != nil {
			h.providerError(w, r, req, err, c.Key)
			return
		}
	}
	c.VerifiedEncryption = verified
	c.Status = "completed"
	version := ack.ProviderVersionID
	c.ProviderVersionID = version
	c.RecoveryVersionsObserved = version != "" && version != "null"
	done, err := h.finishGatewayPut(r.Context(), st, c)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return
	}
	writeEncryptionHeaders(w.Header(), done.Encryption.Selection)
	w.Header().Set("ETag", done.ETag)
	if done.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", done.VersionID)
	}
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

func (h *Handler) gatewayPutRequest(ctx context.Context, r *http.Request, req requestContext, key string, file *os.File, metadata objectstorage.ObjectMetadata, c state.ObjectUploadCompletion) (*http.Request, error) {
	var err error
	ctx, err = h.protectionContext(ctx, req, c.Protection)
	if err != nil {
		return nil, err
	}
	if !c.Protection.Empty() {
		checksum := md5.New() // #nosec G401 -- S3 requires the protocol checksum; this is not a security hash.
		if _, err = io.Copy(checksum, file); err != nil {
			return nil, objectstorage.ErrUnavailable
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, objectstorage.ErrUnavailable
		}
		ctx = objectstorage.WithObjectWriteChecksum(ctx, base64.StdEncoding.EncodeToString(checksum.Sum(nil)))
	}
	sign := objectstorage.SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &r.ContentLength, ContentType: metadata.ContentType, ExpiresIn: int64(api.ObjectGatewayPutURLTTL.Seconds()), CacheControl: metadata.CacheControl, ContentDisposition: metadata.ContentDisposition, ContentEncoding: metadata.ContentEncoding, ContentLanguage: metadata.ContentLanguage, Metadata: metadata.Metadata, Tags: metadata.Tags}
	var signed objectstorage.SignedRequest
	if !c.Encryption.Empty() {
		signer, ok := req.provider.(objectstorage.ObjectEncryptionProvider)
		if !ok {
			return nil, objectstorage.ErrConfiguration
		}
		signed, err = signer.PresignEncryptedPut(h.encryptionContext(ctx, req), req.bucket.PhysicalName, sign, writeConditions(r), c.ID, c.Encryption)
	} else if c.ID != "" {
		if signer, ok := req.provider.(objectstorage.TrackedObjectPresigner); ok {
			signed, err = signer.PresignTrackedPut(ctx, req.bucket.PhysicalName, sign, writeConditions(r), c.ID)
		} else if req.credential.URL != nil {
			signed, err = req.provider.Presign(ctx, req.bucket.PhysicalName, sign)
		} else {
			return nil, objectstorage.ErrUnsupported
		}

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
	if req.credential.URL != nil {
		c, ready := h.loadURLPutReceipt(w, r, req, st)
		return st, c, ready
	}
	c, err := st.BeginTrackedGatewayUpload(r.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: req.bucket.AccountID, AppID: req.bucket.AppID, BucketID: req.bucket.ID, SubjectID: req.credential.ID, Key: key, Bytes: r.ContentLength, ContentType: contentType, RequestID: req.requestID, Status: "pending", Protection: req.protection.Clone(), Encryption: req.encryption.Clone()}, h.registry.Accounting)
	return st, c, h.writeAdmissionError(w, r, req, err)
}
func (h *Handler) dispatchGatewayPut(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectTrackedGatewayUploadStore, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, bool) {
	if req.credential.URL != nil {
		return h.dispatchURLPut(w, r, req, c)
	}
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

func (h *Handler) dispatchURLPut(w http.ResponseWriter, r *http.Request, req requestContext, c state.ObjectUploadCompletion) (state.ObjectUploadCompletion, bool) {
	st, ok := h.store.(state.ObjectURLCapabilityStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return c, false
	}
	intent, err := st.DispatchObjectURLUpload(r.Context(), c.AccountID, c.BucketID, c.ID)
	if errors.Is(err, state.ErrConflict) {
		writeS3Error(w, http.StatusConflict, "OperationAborted", "The signed URL's write is already dispatched, expired or revoked.", r.URL.Path, req.requestID)
		return c, false
	}
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, c.Key)
		return c, false
	}
	return intent, true
}
