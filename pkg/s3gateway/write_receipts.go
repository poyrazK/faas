package s3gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Receipt reads run after SigV4/revocation checks, before provider resolution
// and the new-work flag. They never make an upstream request or an admission.
func (h *Handler) routeWriteReceipt(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	q := operationQuery(r.URL.Query())
	if !q.Has("gregale-upload-id") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	bucket, key, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil || !hasBucket || !hasKey || r.Method != http.MethodGet || r.ContentLength != 0 || !queryKeysOnly(q, "gregale-upload-id") || len(q["gregale-upload-id"]) != 1 {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "Use a signed GET on the original object path with one gregale-upload-id.", r.URL.Path, req.requestID)
		return true
	}
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return true
	}
	id := q.Get("gregale-upload-id")
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id || bucket != req.bucket.Name {
		h.writeReceiptNotFound(w, r, req)
		return true
	}
	st, ok := h.store.(state.ObjectTrackedUploadStore)
	if !ok {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Write receipt storage is temporarily unavailable.", r.URL.Path, req.requestID)
		return true
	}
	c, err := st.GetObjectUploadReceipt(r.Context(), req.bucket.AccountID, req.bucket.AppID, "", req.credential.ID, id)
	if errors.Is(err, state.ErrNotFound) || err == nil && (c.BucketID != req.bucket.ID || c.Key != key || c.Origin != "gateway" && c.Origin != "gateway_copy") {
		h.writeReceiptNotFound(w, r, req)
		return true
	}
	if err != nil {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Write receipt storage is temporarily unavailable.", r.URL.Path, req.requestID)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	if c.Status == "pending" {
		w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectUploadRecoveryRetry.Seconds())))
	}
	_ = json.NewEncoder(w).Encode(state.ViewObjectWriteReceipt(c))
	return true
}

func (h *Handler) writeReceiptNotFound(w http.ResponseWriter, r *http.Request, req requestContext) {
	writeS3Error(w, http.StatusNotFound, "NoSuchUpload", "The specified write receipt does not exist.", r.URL.Path, req.requestID)
}
