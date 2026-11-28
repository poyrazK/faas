package s3gateway

import (
	"crypto/md5" // #nosec G501 -- compares against the S3 ETag, not a security hash.
	"encoding/hex"
	"io"
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
	valid := err == nil && hasBucket && hasKey && bucket == b.Name && key == u.Request.Key && r.Method == u.Request.Method && r.URL.Query().Has("X-Amz-Algorithm") && boundURLQuery(r, u)
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

func boundURLQuery(r *http.Request, u *state.ObjectURLCapability) bool {
	q := operationQuery(r.URL.Query())
	if u.Multipart == nil {
		if u.Request.VersionID != "" {
			return len(q) == 1 && len(q["versionId"]) == 1 && q.Get("versionId") == u.Request.VersionID
		}
		return len(q) == 0
	}
	return len(q) == 2 && len(q["uploadId"]) == 1 && q.Get("uploadId") == u.Multipart.UploadID && len(q["partNumber"]) == 1 && q.Get("partNumber") == strconv.FormatInt(int64(u.Multipart.PartNumber), 10)
}

func fixedURLHeaders(r *http.Request, expected http.Header) bool {
	seen := map[string]bool{}
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		bound := strings.HasPrefix(lower, "x-amz-") && lower != "x-amz-content-sha256" || slices.Contains([]string{"content-type", "cache-control", "content-disposition", "content-encoding", "content-language"}, lower) || r.Method == http.MethodPut && slices.Contains([]string{"if-match", "if-none-match"}, lower)
		if r.Method == http.MethodPut && slices.Contains([]string{"if-modified-since", "if-unmodified-since", "range"}, lower) {
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
	if err != nil || c.BucketID != req.bucket.ID || c.Key != req.credential.URL.Request.Key || !sameURLProtectionSelection(c.Protection, req.protection) || !c.Encryption.Equal(req.encryption) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, req.credential.URL.Request.Key)
		return c, false
	}
	w.Header().Set("X-Gregale-Upload-ID", c.ID)
	if c.Status == "completed" {
		if !completedURLPutMatches(r, c) {
			writeS3Error(w, http.StatusConflict, "OperationAborted", "This signed URL already wrote an object with different content. Request a new signed URL to write again.", r.URL.Path, req.requestID)
			return c, false
		}
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

// completedURLPutMatches reports whether a PUT on a signed URL whose write
// already completed can be a retry of that write. A retry with another body
// used to get the first write's 200 and ETag while nothing was stored (prod
// hunt r79). The size must match; when the stored ETag is a plain MD5 the
// body must hash to it. Other ETags cannot be checked, so a same-size retry
// still gets the saved result.
func completedURLPutMatches(r *http.Request, c state.ObjectUploadCompletion) bool {
	size := r.ContentLength
	chunked := strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "aws-chunked")
	if chunked {
		decoded, err := strconv.ParseInt(r.Header.Get("X-Amz-Decoded-Content-Length"), 10, 64)
		if err != nil {
			return false
		}
		size = decoded
	}
	if size != c.Bytes {
		return false
	}
	want, ok := plainMD5ETag(c.ETag)
	if !ok || chunked || !c.Encryption.Empty() || r.Body == nil {
		return true
	}
	sum := md5.New() // #nosec G401 -- S3 ETag comparison, not a security hash.
	n, err := io.Copy(sum, io.LimitReader(r.Body, c.Bytes+1))
	return err == nil && n == c.Bytes && hex.EncodeToString(sum.Sum(nil)) == want
}

func plainMD5ETag(etag string) (string, bool) {
	v := strings.ToLower(strings.Trim(strings.TrimSpace(etag), `"`))
	if len(v) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(v); err != nil {
		return "", false
	}
	return v, true
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

func sameURLProtectionSelection(a, b state.ObjectWriteProtectionSnapshot) bool {
	a.Enabled, b.Enabled = false, false
	a.Revision, b.Revision = 0, 0
	a.CapturedAt, b.CapturedAt = nil, nil
	a.DefaultRetention, b.DefaultRetention = nil, nil
	return a.Equal(b)
}
