package s3gateway

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Configuration reads/removal need owned durable state, not provider access.
// This runs only after signature and credential revocation checks.
func (h *Handler) routeLifecycleReadOrRemoval(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	q := operationQuery(r.URL.Query())
	q.Del("x-id")
	if !q.Has("lifecycle") || r.Method != http.MethodGet && r.Method != http.MethodDelete {
		return false
	}
	bucket, _, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil || !hasBucket || hasKey || !queryKeysOnly(q, "lifecycle") || len(q["lifecycle"]) != 1 || q.Get("lifecycle") != "" {
		h.lifecycleError(w, r, req, objectstorage.ErrInvalid)
		return true
	}
	if bucket != req.bucket.Name {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", r.URL.Path, req.requestID)
		return true
	}
	if hasUnsupportedS3Semantics(r) {
		h.unsupported(w, r, req.requestID)
		return true
	}
	h.bucketLifecycle(w, r, req)
	return true
}

func (h *Handler) bucketLifecycle(w http.ResponseWriter, r *http.Request, req requestContext) {
	svc, ok := h.lifecyclePolicyService(w, r, req)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		h.putBucketLifecycle(w, r, req, svc)
		return
	}
	if isStreamingPayloadHash(req.signature.PayloadHash) {
		h.lifecycleError(w, r, req, objectstorage.ErrInvalid)
		return
	}
	if _, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, 0); err != nil {
		h.lifecycleError(w, r, req, err)
		return
	}
	if r.Method == http.MethodDelete {
		if _, err := svc.Write(r.Context(), req.bucket, nil); err != nil {
			h.lifecycleError(w, r, req, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.getBucketLifecycle(w, r, req, svc)
}
func (h *Handler) lifecyclePolicyService(w http.ResponseWriter, r *http.Request, req requestContext) (objectstorage.LifecyclePolicyService, bool) {
	svc := objectstorage.LifecyclePolicyService{}
	permission := state.ObjectBucketPermissionWrite
	if r.Method == http.MethodGet {
		permission = state.ObjectBucketPermissionRead
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return svc, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		h.unsupported(w, r, req.requestID)
		return svc, false
	}
	if err := objectstorage.ValidateObjectTaggingRequest(r); err != nil {
		h.lifecycleError(w, r, req, err)
		return svc, false
	}
	if len(r.Header.Values("X-Amz-Transition-Default-Minimum-Object-Size")) != 0 {
		h.unsupported(w, r, req.requestID)
		return svc, false
	}
	st, ok := h.store.(state.ObjectLifecycleStore)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return svc, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return objectstorage.LifecyclePolicyService{Store: st, Provider: req.provider}, true
}
func (h *Handler) getBucketLifecycle(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.LifecyclePolicyService) {
	p, err := svc.Read(r.Context(), req.bucket)
	if err != nil {
		h.lifecycleError(w, r, req, err)
		return
	}
	if len(p.Rules) == 0 {
		writeS3Error(w, http.StatusNotFound, "NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist.", r.URL.Path, req.requestID)
		return
	}
	body, err := objectstorage.MarshalObjectLifecycleXML(p.Rules)
	if err != nil {
		h.lifecycleError(w, r, req, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
func (h *Handler) putBucketLifecycle(w http.ResponseWriter, r *http.Request, req requestContext, svc objectstorage.LifecyclePolicyService) {
	body, err := readVerifiedRequestBody(w, r, req.signature.PayloadHash, api.MaxObjectLifecycleBodyBytes)
	if err != nil {
		h.lifecycleError(w, r, req, err)
		return
	}
	rules, err := objectstorage.ParseObjectLifecycleXML(body)
	if err != nil {
		h.lifecycleError(w, r, req, err)
		return
	}
	if _, err = svc.Write(r.Context(), req.bucket, rules); err != nil {
		h.lifecycleError(w, r, req, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
func (h *Handler) lifecycleError(w http.ResponseWriter, r *http.Request, req requestContext, err error) {
	if h.writeAWSChunkedError(w, r, req.requestID, err) {
		return
	}
	switch {
	case errors.Is(err, objectstorage.ErrUnsupported):
		h.unsupported(w, r, req.requestID)
	case errors.Is(err, objectstorage.ErrInvalid):
		writeS3Error(w, http.StatusBadRequest, "MalformedXML", "The lifecycle configuration or request is invalid.", r.URL.Path, req.requestID)
	case errors.Is(err, state.ErrConflict):
		writeS3Error(w, http.StatusConflict, "OperationAborted", "A lifecycle scan holds the policy; retry after it releases its lease.", r.URL.Path, req.requestID)
	default:
		h.providerError(w, r, req, err, "")
	}
}
