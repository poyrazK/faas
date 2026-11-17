package s3gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// overlayPrefixProbes bounds the source common prefixes one merged page
// checks for a visible key. A page that reaches it ends there with a
// continuation token, as a page cut at a provider horizon does.
const overlayPrefixProbes = 16

// overlayPrefixProbePage is the source listing one prefix check reads.
const overlayPrefixProbePage = 1000

// overlayPrefixVisible reports whether a source common prefix, which the
// branch does not list itself, holds a key the branch reads
// (ADR-20261031193635 amendment 2026-11-17). Under live_parent a prefix
// with no tombstone under it always does: the source listed it, so it holds
// a current object. Otherwise one source listing of the prefix decides:
// visible when it holds a key that is not tombstoned (for as_of, a key live
// at the branch point). A prefix the listing cannot decide within one page
// lists, as before.
func (h *Handler) overlayPrefixVisible(w http.ResponseWriter, r *http.Request, req, through requestContext, prefix string) (bool, error) {
	ctx := r.Context()
	if through.overlayAsOf.IsZero() {
		if store, ok := h.store.(state.ObjectBucketOverlayTombstonePrefixStore); ok {
			deleted, err := store.ObjectBucketOverlayTombstonesUnder(ctx, req.bucket.ID, prefix)
			if err != nil {
				return false, objectstorage.ErrUnavailable
			}
			if !deleted {
				return true, nil
			}
		}
	}
	list := objectstorage.ObjectListRequest{Prefix: prefix, Limit: overlayPrefixProbePage}
	var entries []overlayListEntry
	more := false
	if through.overlayAsOf.IsZero() {
		if !h.recordProviderRequest(w, r, req) {
			return false, errOverlayListAnswered
		}
		page, err := listProviderObjects(ctx, through.provider, through.bucket.PhysicalName, list)
		if err != nil {
			return false, err
		}
		entries, more = overlayListEntries(page), page.NextCursor != ""
	} else {
		lister := through.provider.(objectstorage.ObjectVersionLister)
		listing, err := listAsOf(ctx, lister, through.bucket.PhysicalName, list, through.overlayAsOf,
			func() bool { return h.recordProviderRequest(w, r, req) })
		if err != nil {
			return false, err
		}
		entries, more = listing.entries, listing.horizon != ""
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.key)
	}
	store, _ := h.store.(state.ObjectBucketOverlayStore)
	hidden, err := overlayTombstones(r, store, req.bucket.ID, keys)
	if err != nil {
		return false, objectstorage.ErrUnavailable
	}
	for _, key := range keys {
		if !hidden[key] {
			return true, nil
		}
	}
	return more, nil
}

// overlayTombstones reports which keys the branch deleted, in lookups the
// store accepts: a source listing may hold more keys than one lookup takes
// (an as_of listing reads whole provider pages past its limit).
func overlayTombstones(r *http.Request, store state.ObjectBucketOverlayStore, bucketID string, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(keys); start += state.ObjectBucketOverlayTombstoneBatch {
		batch := keys[start:min(start+state.ObjectBucketOverlayTombstoneBatch, len(keys))]
		hidden, err := store.ObjectBucketOverlayTombstones(r.Context(), bucketID, batch)
		if err != nil {
			return nil, err
		}
		for key := range hidden {
			out[key] = true
		}
	}
	return out, nil
}
