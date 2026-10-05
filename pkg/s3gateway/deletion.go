package s3gateway

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

// SDK invocation IDs and the Gregale retry token are trusted only when signed.
func deletionRequestID(r *http.Request, req requestContext) (string, error) {
	for _, name := range []string{"X-Gregale-Delete-Id", "Amz-Sdk-Invocation-Id"} {
		values := r.Header.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || !slices.Contains(strings.Split(req.signature.SignedHeader, ";"), strings.ToLower(name)) {
			return "", objectstorage.ErrInvalid
		}
		parsed, e := uuid.Parse(values[0])
		if e != nil || parsed.String() != values[0] {
			return "", objectstorage.ErrInvalid
		}
		return parsed.String(), nil
	}
	return uuid.NewString(), nil
}
func bulkDeletionRequestID(id string, index int) string {
	// id has passed deletionRequestID's canonical UUID validation.
	return uuid.NewSHA1(uuid.MustParse(id), []byte("bulk-entry:"+strconv.Itoa(index))).String()
}
func (h *Handler) deleteObjectIntent(ctx context.Context, req requestContext, key, selector, id string) (state.ObjectDeletion, error) {
	st, _ := h.store.(state.ObjectDeletionStore)
	before := func(ctx context.Context) error {
		if h.requestMetrics != nil {
			return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
		}
		return nil
	}
	svc := objectstorage.DeletionService{Store: st, Provider: req.provider, BeforeRequest: before}
	j, e := objectstorageactivity.Execute(ctx, h.store, req.bucket, func(mutationCtx context.Context) (state.ObjectDeletion, error) {
		j, err := svc.Start(mutationCtx, req.bucket, key, selector, id, h.registry.Accounting)
		if err == nil && j.State != "completed" {
			err = objectstorage.ErrUnavailable
		}
		return j, err
	})
	return j, e
}
func (h *Handler) deleteCurrentObject(w http.ResponseWriter, r *http.Request, req requestContext, key string) {
	if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	if e := objectstorage.ValidateObjectDeleteRequest(r); e != nil {
		h.providerError(w, r, req, e, key)
		return
	}
	id, e := deletionRequestID(r, req)
	if e != nil {
		h.providerError(w, r, req, e, key)
		return
	}
	j, e := h.deleteObjectIntent(r.Context(), req, key, "", id)
	w.Header().Set("X-Gregale-Delete-Id", id)
	if e != nil {
		h.providerError(w, r, req, e, key)
		return
	}
	writeDeletionHeaders(w, j)
	w.WriteHeader(http.StatusNoContent)
}
func writeDeletionHeaders(w http.ResponseWriter, j state.ObjectDeletion) {
	if j.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", j.VersionID)
	}
	if j.DeleteMarker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
	}
}
func (h *Handler) deleteSelectedVersion(ctx context.Context, r *http.Request, req requestContext, key, selector string) (api.ObjectVersionDeleteResult, string, error) {
	id, e := deletionRequestID(r, req)
	if e != nil {
		return api.ObjectVersionDeleteResult{}, "", e
	}
	j, e := h.deleteObjectIntent(ctx, req, key, selector, id)
	return api.ObjectVersionDeleteResult{VersionID: j.VersionID, DeleteMarker: j.DeleteMarker}, id, e
}
