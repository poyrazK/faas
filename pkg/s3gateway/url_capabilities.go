package s3gateway

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func setURLDownloadHeaders(headers http.Header, r *http.Request, req requestContext) {
	if req.credential.URL != nil && r.Method == http.MethodGet {
		headers.Set("Content-Disposition", "attachment")
		headers.Set("Content-Type", "application/octet-stream")
		headers.Set("X-Content-Type-Options", "nosniff")
	}
}

func (h *Handler) boundURLRequest(w http.ResponseWriter, r *http.Request, c state.ObjectS3Credential, b state.ObjectBucket, requestID string) bool {
	if c.URL == nil {
		return true
	}
	u := c.URL
	bucket, key, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	valid := err == nil && hasBucket && hasKey && bucket == b.Name && key == u.Request.Key && r.Method == u.Request.Method && r.URL.Query().Has("X-Amz-Algorithm") && len(operationQuery(r.URL.Query())) == 0
	if u.Request.Method == http.MethodPut {
		valid = valid && u.Request.SizeBytes != nil && *u.Request.SizeBytes == r.ContentLength
	} else {
		valid = valid && r.ContentLength == 0
	}
	expected, e := objectstorage.PublicSignedObjectHeaders(objectstorage.SignRequest(u.Request))
	valid = valid && e == nil && fixedURLHeaders(r, expected)
	if !valid {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "The signed URL is bound to its original object request.", r.URL.Path, requestID)
	}
	return valid
}

func fixedURLHeaders(r *http.Request, expected http.Header) bool {
	seen := map[string]bool{}
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		bound := strings.HasPrefix(lower, "x-amz-") && lower != "x-amz-content-sha256" || slices.Contains([]string{"content-type", "cache-control", "content-disposition", "content-encoding", "content-language"}, lower)
		if r.Method == http.MethodPut && slices.Contains([]string{"if-match", "if-none-match", "if-modified-since", "if-unmodified-since", "range"}, lower) {
			return false
		}
		if !bound {
			continue
		}
		want, exists := expected[http.CanonicalHeaderKey(name)]
		if !exists || seen[lower] || len(values) != 1 || len(want) != 1 || values[0] != want[0] {
			return false
		}
		seen[lower] = true
	}
	for name := range expected {
		if !seen[strings.ToLower(name)] {
			return false
		}
	}
	return true
}

func (h *Handler) loadURLPutReceipt(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectTrackedGatewayUploadStore) (state.ObjectUploadCompletion, bool) {
	// Staging may outlive the URL or a grant. Recheck before native signing
	// and key probes; the atomic dispatch checks authority again afterward.
	if _, _, err := h.store.ResolveObjectS3Credential(r.Context(), req.credential.AccessKeyID); err != nil {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "The signed URL is expired or its issuer permission was revoked.", r.URL.Path, req.requestID)
		return state.ObjectUploadCompletion{}, false
	}
	c, err := st.GetObjectUploadReceipt(r.Context(), req.bucket.AccountID, req.bucket.AppID, "", req.credential.ID, req.credential.URL.ReceiptID)
	if err != nil || c.BucketID != req.bucket.ID || c.Key != req.credential.URL.Request.Key || !c.Encryption.Equal(req.encryption) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, req.credential.URL.Request.Key)
		return c, false
	}
	w.Header().Set("X-Gregale-Upload-ID", c.ID)
	if c.Status == "completed" {
		writeEncryptionHeaders(w.Header(), c.Encryption.Selection)
		w.Header().Set("ETag", c.ETag)
		if c.VersionID != "" {
			w.Header().Set("X-Amz-Version-Id", c.VersionID)
		}
		w.WriteHeader(http.StatusOK)
		return c, false
	}
	if c.Status != "pending" || c.WritePhase != state.ObjectUploadPrepared {
		if c.Status == "pending" {
			w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectUploadRecoveryRetry.Seconds())))
		}
		writeS3Error(w, http.StatusConflict, "OperationAborted", "This signed URL's write receipt is pending or failed. Inspect the receipt before retrying.", r.URL.Path, req.requestID)
		return c, false
	}
	return c, true
}

func (h *Handler) replayURLPut(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	if req.credential.URL == nil {
		return false
	}
	st, ok := h.store.(state.ObjectTrackedGatewayUploadStore)
	if !ok {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, req.credential.URL.Request.Key)
		return true
	}
	_, ready := h.loadURLPutReceipt(w, r, req, st)
	return !ready
}
