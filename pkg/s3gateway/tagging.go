package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) objectTags(w http.ResponseWriter, r *http.Request, req requestContext, key string) {
	permission := state.ObjectBucketPermissionWrite
	if r.Method == http.MethodGet {
		permission = state.ObjectBucketPermissionRead
	} else if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		h.unsupported(w, r, req.requestID)
		return
	}
	if !h.require(w, req, permission, r.URL.Path) {
		return
	}
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	selector := q.Get("versionId")
	if parseErr != nil || q.Get("tagging") != "" || q.Has("versionId") && !state.ValidObjectVersionID(selector) {
		h.taggingError(w, r, req, objectstorage.ErrInvalid, key, selector)
		return
	}
	if err := objectstorage.ValidateObjectTaggingRequest(r); err != nil {
		h.taggingError(w, r, req, err, key, selector)
		return
	}
	tags, ok := h.taggingInput(w, r, req)
	if !ok {
		return
	}
	call := func(callCtx context.Context) (api.ObjectTaggingResult, error) {
		return h.taggingService(req, key).Do(callCtx, req.bucket, r.Method, key, selector, tags)
	}
	var out api.ObjectTaggingResult
	var err error
	if r.Method == http.MethodGet {
		out, err = call(r.Context())
	} else {
		out, err = objectstorageactivity.Execute(r.Context(), h.store, req.bucket, call)
	}
	if err != nil {
		h.taggingError(w, r, req, err, key, selector)
		return
	}
	writeObjectTaggingResult(w, r.Method, req.requestID, out)
}

func writeObjectTaggingResult(w http.ResponseWriter, method, requestID string, out api.ObjectTaggingResult) {
	if out.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", out.VersionID)
	}
	switch method {
	case http.MethodGet:
		writeS3XML(w, http.StatusOK, requestID, objectTaggingResult{XMLNS: s3XMLNamespace, Tags: objectTagSet(out.Tags)})
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) taggingService(req requestContext, key string) objectstorage.TaggingService {
	refs, _ := h.store.(state.ObjectVersionReferenceStore)
	return objectstorage.TaggingService{References: refs, Provider: req.provider, BeforeRequest: func(ctx context.Context) error {
		if err := h.store.AdmitObjectURL(ctx, req.bucket.AccountID, req.bucket.ID, key, 0, false, h.registry.Accounting); err != nil {
			return err
		}
		if h.requestMetrics != nil {
			return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
		}
		return nil
	}}
}

func (h *Handler) taggingInput(w http.ResponseWriter, r *http.Request, req requestContext) (map[string]string, bool) {
	if r.Method != http.MethodPut {
		return nil, true
	}
	tags, err := decodeObjectTags(w, r, req.signature.PayloadHash)
	if err == nil {
		return tags, true
	}
	if !h.writeAWSChunkedError(w, r, req.requestID, err) {
		writeS3Error(w, http.StatusBadRequest, "InvalidTag", "The object tags are invalid.", r.URL.Path, req.requestID)
	}
	return nil, false
}

func (h *Handler) taggingError(w http.ResponseWriter, r *http.Request, req requestContext, err error, key, selector string) {
	if errors.Is(err, objectstorage.ErrObjectNotTaggable) {
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified object version does not support tagging.", r.URL.Path, req.requestID)
	} else if selector != "" && (errors.Is(err, objectstorage.ErrNotFound) || errors.Is(err, state.ErrNotFound)) {
		writeS3Error(w, http.StatusNotFound, "NoSuchVersion", "The specified version does not exist.", r.URL.Path, req.requestID)
	} else if errors.Is(err, state.ErrObjectBudget) || errors.Is(err, state.ErrObjectCapacity) || errors.Is(err, state.ErrObjectUsageStale) {
		h.writeAdmissionError(w, r, req, err)
	} else {
		h.providerError(w, r, req, err, key)
	}
}
