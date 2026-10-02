package s3gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) deleteVersion(w http.ResponseWriter, r *http.Request, req requestContext, key, id string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	if err := objectstorage.ValidateObjectDeleteRequest(r); err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	var result api.ObjectVersionDeleteResult
	var err error
	if id == "null" {
		var token string
		result, token, err = h.deleteNullVersion(r.Context(), r, req, key)
		if token != "" {
			w.Header().Set("X-Gregale-Delete-Id", token)
		}
	} else {
		result, err = h.deleteOwnedVersion(r.Context(), req, key, id)
	}
	if errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, http.StatusNotFound, "NoSuchVersion", "The specified version does not exist.", r.URL.Path, req.requestID)
		return
	}
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	w.Header().Set("X-Amz-Version-Id", result.VersionID)
	if result.DeleteMarker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteOwnedVersion(ctx context.Context, req requestContext, key, id string) (api.ObjectVersionDeleteResult, error) {
	st, _ := h.store.(state.ObjectVersionReferenceStore)
	before := func(ctx context.Context) error {
		if h.requestMetrics == nil {
			return nil
		}
		return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
	}
	return objectstorage.DeleteOwnedObjectVersion(ctx, st, req.provider, req.bucket, key, id, before)
}
