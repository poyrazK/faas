package s3gateway

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) deleteVersion(w http.ResponseWriter, r *http.Request, req requestContext, key, id string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	if !state.ValidObjectVersionID(id) || len(r.URL.Query()["versionId"]) != 1 {
		h.providerError(w, r, req, objectstorage.ErrInvalid, key)
		return
	}
	if err := objectstorage.ValidateObjectDeleteRequest(r); err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	result, token, err := h.deleteSelectedVersion(r.Context(), r, req, key, id)
	if token != "" {
		w.Header().Set("X-Gregale-Delete-Id", token)
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
