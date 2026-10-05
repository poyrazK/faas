package s3gateway

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func copySourceContext(req requestContext) requestContext {
	if req.copySource != nil {
		req.bucket = *req.copySource
	}
	req.copySource, req.copyGrantID = nil, ""
	return req
}

func (h *Handler) authorizeGatewayCopySource(w http.ResponseWriter, r *http.Request, req requestContext, source gatewayCopySource, part bool) (requestContext, bool) {
	id, err := uuid.Parse(source.Bucket)
	canonicalID := err == nil && id != uuid.Nil && id.String() == source.Bucket
	// Public UUID selectors take precedence even when a legacy bucket name
	// happens to equal another bucket's ID. Never reinterpret a revoked source.
	if source.Bucket == req.bucket.ID || source.Bucket == req.bucket.Name && !canonicalID {
		return req, h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path)
	}
	if !canonicalID {
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified copy source does not exist.", r.URL.Path, req.requestID)
		return req, false
	}
	sources, ok := h.store.(state.ObjectS3CopySourceStore)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return req, false
	}
	grant, bucket, err := sources.ResolveObjectS3CopySource(r.Context(), req.bucket.AccountID, req.credential.ID, source.Bucket, source.Key)
	if errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified copy source does not exist.", r.URL.Path, req.requestID)
		return req, false
	}
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, source.Key)
		return req, false
	}
	_, capable := req.provider.(objectstorage.CrossBucketTrackedObjectCopier)
	if part {
		_, capable = req.provider.(objectstorage.CrossBucketMultipartPartCopier)
	}
	if !capable {
		h.unsupported(w, r, req.requestID)
		return req, false
	}
	req.copySource, req.copyGrantID = &bucket, grant.ID
	return req, true
}
