package s3gateway

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Copy-through (ADR-20261031193635 amendment 2026-11-14). A copy inside a
// branch bucket whose source key only the parent holds copies from the
// parent, server side, as the branch would read it: the current object of a
// live_parent overlay, the version at the branch point of an as_of one.
//
// The authority is an ordinary copy grant on the parent, which apid gives
// each credential it binds to the branch bucket; the provider copies across
// buckets of one backend, which a branch bucket shares with its parent. A
// credential without a grant, a key the branch deleted, or a key the
// parent did not hold at the branch point copies nothing: the copy answers
// the branch's own 404.

// overlayCopySource redirects a same-bucket copy to the overlay's source
// when the branch does not hold key. It reports false once it has written a
// response.
func (h *Handler) overlayCopySource(w http.ResponseWriter, r *http.Request, req requestContext, source gatewayCopySource, part bool) (requestContext, bool) {
	if source.VersionID != "" {
		return req, true // a version of the branch's own object
	}
	through, overlaid, err := h.overlaySource(r.Context(), req)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, source.Key)
		return req, false
	}
	if !overlaid {
		return req, true
	}
	if !h.recordProviderRequest(w, r, req) {
		return req, false
	}
	own, err := listProviderObjects(r.Context(), req.provider, req.bucket.PhysicalName, objectstorage.ObjectListRequest{Prefix: source.Key, Limit: 1})
	if err != nil {
		h.providerError(w, r, req, err, source.Key)
		return req, false
	}
	if len(own.Items) > 0 && own.Items[0].Key == source.Key {
		return req, true // the branch's own object
	}
	store, _ := h.store.(state.ObjectBucketOverlayStore)
	hidden, err := store.ObjectBucketOverlayTombstones(r.Context(), req.bucket.ID, []string{source.Key})
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, source.Key)
		return req, false
	}
	if hidden[source.Key] {
		return req, true
	}
	native, found, answered := h.overlayVersion(w, r, req, through, source.Key)
	if answered {
		return req, false
	}
	if !found {
		return req, true
	}
	sources, ok := h.store.(state.ObjectS3CopySourceStore)
	if !ok {
		return req, true
	}
	grant, bucket, err := sources.ResolveObjectS3CopySource(r.Context(), req.bucket.AccountID, req.credential.ID, through.bucket.ID, source.Key)
	if errors.Is(err, state.ErrNotFound) {
		return req, true
	}
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, source.Key)
		return req, false
	}
	_, capable := req.provider.(objectstorage.CrossBucketTrackedObjectCopier)
	if part {
		_, capable = req.provider.(objectstorage.CrossBucketMultipartPartCopier)
	}
	if !capable || bucket.ID != through.bucket.ID {
		return req, true
	}
	req.copySource, req.copyGrantID, req.overlayCopyVersion = &bucket, grant.ID, native
	return req, true
}
