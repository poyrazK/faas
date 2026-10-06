package s3gateway

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Notification intent is owned state and needs no provider RPC.
func (h *Handler) routeBucketNotifications(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	q := operationQuery(r.URL.Query())
	q.Del("x-id")
	if !q.Has("notification") {
		return false
	}
	b, _, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil || !hasBucket || hasKey || !queryKeysOnly(q, "notification") || len(q["notification"]) != 1 || q.Get("notification") != "" {
		h.notificationError(w, r, req, objectstorage.ErrInvalid)
		return true
	}
	if b != req.bucket.Name {
		writeS3Error(w, 404, "NoSuchBucket", "The specified bucket does not exist.", r.URL.Path, req.requestID)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		h.unsupported(w, r, req.requestID)
		return true
	}
	permission := state.ObjectBucketPermissionWrite
	if r.Method == http.MethodGet {
		permission = state.ObjectBucketPermissionRead
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return true
	}
	if !h.validNotificationHeaders(w, r, req) {
		return true
	}
	if !h.validateRequestSemantics(w, r, req, false, q) {
		return true
	}
	if isStreamingPayloadHash(req.signature.PayloadHash) {
		h.notificationError(w, r, req, objectstorage.ErrUnsupported)
		return true
	}
	st, ok := h.store.(state.ObjectNotificationStore)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	h.bucketNotifications(w, r, req, st)
	return true
}
func (h *Handler) validNotificationHeaders(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	// Expected owner is an account UUID in the Gregale profile. Validate before
	// removing it from the common unsupported-semantics check.
	if values := r.Header.Values("X-Amz-Expected-Bucket-Owner"); len(values) > 0 && (len(values) != 1 || values[0] != req.bucket.AccountID) {
		writeS3Error(w, 403, "AccessDenied", "Access Denied.", r.URL.Path, req.requestID)
		return false
	}
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Del("X-Amz-Expected-Bucket-Owner")
	if err := objectstorage.ValidateObjectTaggingRequest(copy); err != nil {
		h.notificationError(w, r, req, err)
		return false
	}
	skip := r.Header.Values("X-Amz-Skip-Destination-Validation")
	if hasUnsupportedS3Semantics(copy) || len(skip) > 0 && (len(skip) != 1 || skip[0] != "false" || r.Method != http.MethodPut) {
		h.unsupported(w, r, req.requestID)
		return false
	}
	return true
}
func (h *Handler) bucketNotifications(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectNotificationStore) {
	limit := int64(0)
	if r.Method == http.MethodPut {
		limit = api.MaxObjectNotificationBodyBytes
	}
	body, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, limit)
	if err != nil {
		h.notificationError(w, r, req, err)
		return
	}
	if r.Method == http.MethodPut {
		h.replaceBucketNotifications(w, r, req, st, body)
		return
	}
	p, err := st.GetObjectBucketNotifications(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID)
	if err != nil {
		h.notificationError(w, r, req, err)
		return
	}
	body, err = objectstorage.MarshalObjectNotificationsXML(p.Rules)
	if err != nil {
		h.notificationError(w, r, req, err)
		return
	}
	h.touchCredential(r.Context(), req.credential.ID)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
func (h *Handler) replaceBucketNotifications(w http.ResponseWriter, r *http.Request, req requestContext, st state.ObjectNotificationStore, body []byte) {
	rules, err := objectstorage.ParseObjectNotificationsXML(body)
	if err != nil {
		h.notificationError(w, r, req, err)
		return
	}
	if len(rules) > 0 && !h.enabled() {
		h.notificationError(w, r, req, objectstorage.ErrUnavailable)
		return
	}
	if _, err = st.SetObjectBucketNotifications(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID, rules); err != nil {
		h.notificationError(w, r, req, err)
		return
	}
	h.touchCredential(r.Context(), req.credential.ID)
	w.WriteHeader(http.StatusOK)
}
func (h *Handler) notificationError(w http.ResponseWriter, r *http.Request, req requestContext, err error) {
	if h.writeAWSChunkedError(w, r, req.requestID, err) {
		return
	}
	switch {
	case errors.Is(err, objectstorage.ErrUnsupported):
		h.unsupported(w, r, req.requestID)
	case errors.Is(err, objectstorage.ErrInvalid), errors.Is(err, state.ErrObjectNotificationInvalid):
		writeS3Error(w, 400, "InvalidArgument", "The notification configuration or destination is invalid.", r.URL.Path, req.requestID)
	default:
		h.providerError(w, r, req, err, "")
	}
}
