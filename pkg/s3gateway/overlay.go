package s3gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Environment branch buckets (ADR-20261031193635 slice 3). A branch bucket
// has an overlay on its parent's bucket: a GET or HEAD of a key the branch
// never wrote, and has not deleted, is read through from the source; a
// DELETE records a tombstone that hides the source's key; ListObjectsV2
// merges both buckets. The branch's writes land in the branch bucket only.
// Version, tagging, attribute and copy requests see the branch's own objects.

const (
	overlayCacheTTL     = 30 * time.Second
	overlayCacheEntries = 4096
	// overlayListCursorPrefix marks a merged listing's continuation token,
	// which carries the start-after boundary of the next page.
	overlayListCursorPrefix = "gregale-overlay-1."
)

type overlayCacheEntry struct {
	overlay state.ObjectBucketOverlay
	found   bool
	at      time.Time
}

// overlayCache remembers, per bucket, whether it overlays a source. An
// overlay is written before its bucket is provisioned and never changes its
// source, so only the source bucket's state can go stale, for at most the
// TTL; a read through to a deleted source finds nothing.
type overlayCache struct {
	mu      sync.Mutex
	entries map[string]overlayCacheEntry
}

func (c *overlayCache) get(bucketID string, now time.Time) (overlayCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[bucketID]
	if !ok || now.Sub(entry.at) >= overlayCacheTTL {
		return overlayCacheEntry{}, false
	}
	return entry, true
}

func (c *overlayCache) put(bucketID string, entry overlayCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= overlayCacheEntries {
		c.entries = map[string]overlayCacheEntry{}
	}
	c.entries[bucketID] = entry
}

// bucketOverlay returns the bucket's overlay when it has one with a live
// source.
func (h *Handler) bucketOverlay(ctx context.Context, bucket state.ObjectBucket) (state.ObjectBucketOverlay, bool, error) {
	store, ok := h.store.(state.ObjectBucketOverlayStore)
	if !ok {
		return state.ObjectBucketOverlay{}, false, nil
	}
	now := h.now()
	entry, cached := h.overlays.get(bucket.ID, now)
	if !cached {
		overlay, err := store.ObjectBucketOverlay(ctx, bucket.ID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			entry = overlayCacheEntry{at: now}
		case err != nil:
			return state.ObjectBucketOverlay{}, false, err
		default:
			entry = overlayCacheEntry{overlay: overlay, found: true, at: now}
		}
		h.overlays.put(bucket.ID, entry)
	}
	if !entry.found || entry.overlay.AccountID != bucket.AccountID {
		return state.ObjectBucketOverlay{}, false, nil
	}
	return entry.overlay, true, nil
}

// overlaySource is the request context that reads the overlay's source
// bucket. It reports false when the bucket overlays nothing it can read:
// no overlay, a deleted or unready source, or a consistency this gateway
// does not serve.
func (h *Handler) overlaySource(ctx context.Context, req requestContext) (requestContext, bool, error) {
	overlay, ok, err := h.bucketOverlay(ctx, req.bucket)
	if err != nil || !ok {
		return requestContext{}, false, err
	}
	source := overlay.Source
	if source.ID == "" || source.State != "ready" || source.AccountID != req.bucket.AccountID ||
		overlay.Consistency != state.ObjectBucketOverlayLiveParent {
		return requestContext{}, false, nil
	}
	backend, err := h.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return requestContext{}, false, objectstorage.ErrUnavailable
	}
	through := req
	through.bucket, through.provider = source, backend.Provider
	through.encryptionConfig, through.objectLockConfig = backend.Encryption, backend.ObjectLock
	return through, true, nil
}

// readThroughOverlay serves a GET or HEAD the branch bucket answered 404 from
// the overlay's source. It reports false when the request is not the
// overlay's to serve, and the caller then answers with the branch's 404.
// The read is admitted and metered against the branch bucket.
func (h *Handler) readThroughOverlay(w http.ResponseWriter, r *http.Request, req requestContext, key string) bool {
	through, ok, err := h.overlaySource(r.Context(), req)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return true
	}
	if !ok {
		return false
	}
	store, _ := h.store.(state.ObjectBucketOverlayStore)
	hidden, err := store.ObjectBucketOverlayTombstones(r.Context(), req.bucket.ID, []string{key})
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return true
	}
	if hidden[key] {
		return false
	}
	upstream, err := h.prepareGatewayRead(r, through, key, "")
	if err != nil {
		h.providerError(w, r, req, err, key)
		return true
	}
	if !h.recordProviderRequest(w, r, req) {
		return true
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return true
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	// The source's versions are not the branch's: a source delete marker is
	// just a missing key, and no version ID is returned.
	if marker := response.Header.Get("X-Amz-Delete-Marker"); marker != "" {
		h.providerError(w, r, req, objectstorage.ErrNotFound, key)
		return true
	}
	if !validReadChecksumHeaders(response.Header) {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return true
	}
	served := req
	served.provider, served.encryptionConfig = through.provider, through.encryptionConfig
	h.writeGatewayRead(w, r, served, key, response)
	return true
}

// tombstoneOverlayKey records, before the branch's own object is deleted,
// that key no longer reads through to the source.
func (h *Handler) tombstoneOverlayKey(ctx context.Context, req requestContext, key string) error {
	if _, ok, err := h.bucketOverlay(ctx, req.bucket); err != nil || !ok {
		return err
	}
	store, _ := h.store.(state.ObjectBucketOverlayStore)
	if err := store.AddObjectBucketOverlayTombstones(ctx, req.bucket.ID, []string{key}); err != nil {
		return objectstorage.ErrUnavailable
	}
	return nil
}

func encodeOverlayListCursor(after string) string {
	return overlayListCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte(after))
}

func decodeOverlayListCursor(token string) (string, bool) {
	encoded, ok := strings.CutPrefix(token, overlayListCursorPrefix)
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > 1024+utf8.UTFMax || !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
}

// overlayListEntry is one key or common prefix of a listing page, in the
// single lexicographic order S3 lists them in.
type overlayListEntry struct {
	key    string
	prefix bool
	object objectstorage.Object
}

// after is the start-after boundary past this entry. Past a common prefix
// it is the largest key the prefix can hold, so the next page does not roll
// the same prefix up again.
func (e overlayListEntry) after() string {
	if e.prefix {
		return e.key + string(utf8.MaxRune)
	}
	return e.key
}

func overlayListEntries(page objectstorage.ObjectPage) []overlayListEntry {
	out := make([]overlayListEntry, 0, len(page.Items)+len(page.CommonPrefixes))
	i, j := 0, 0
	for i < len(page.Items) || j < len(page.CommonPrefixes) {
		if j >= len(page.CommonPrefixes) || i < len(page.Items) && page.Items[i].Key < page.CommonPrefixes[j] {
			out = append(out, overlayListEntry{key: page.Items[i].Key, object: page.Items[i]})
			i++
			continue
		}
		out = append(out, overlayListEntry{key: page.CommonPrefixes[j], prefix: true})
		j++
	}
	return out
}

// listOverlayObjects lists one merged page of a branch bucket and its
// source. Both lists start after the same boundary; the branch's entry wins
// a key both hold, a tombstoned source key is skipped, and the page stops at
// the first truncated list's last entry so that no key is skipped. A source
// common prefix whose keys the branch all deleted still lists.
func (h *Handler) listOverlayObjects(w http.ResponseWriter, r *http.Request, req, through requestContext, list objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	list.Cursor = ""
	if !h.recordProviderRequest(w, r, req) {
		return objectstorage.ObjectPage{}, errOverlayListAnswered
	}
	branchPage, err := listProviderObjects(r.Context(), req.provider, req.bucket.PhysicalName, list)
	if err != nil {
		return objectstorage.ObjectPage{}, err
	}
	if !h.recordProviderRequest(w, r, req) {
		return objectstorage.ObjectPage{}, errOverlayListAnswered
	}
	sourcePage, err := listProviderObjects(r.Context(), through.provider, through.bucket.PhysicalName, list)
	if err != nil {
		return objectstorage.ObjectPage{}, err
	}
	branch, source := overlayListEntries(branchPage), overlayListEntries(sourcePage)
	var keys []string
	for _, e := range source {
		if !e.prefix {
			keys = append(keys, e.key)
		}
	}
	store, _ := h.store.(state.ObjectBucketOverlayStore)
	hidden, err := store.ObjectBucketOverlayTombstones(r.Context(), req.bucket.ID, keys)
	if err != nil {
		return objectstorage.ObjectPage{}, objectstorage.ErrUnavailable
	}
	var horizon *overlayListEntry
	for _, l := range []struct {
		entries   []overlayListEntry
		truncated bool
	}{{branch, branchPage.NextCursor != ""}, {source, sourcePage.NextCursor != ""}} {
		if !l.truncated {
			continue
		}
		if len(l.entries) == 0 {
			return objectstorage.ObjectPage{}, objectstorage.ErrUnavailable
		}
		if last := l.entries[len(l.entries)-1]; horizon == nil || last.key < horizon.key {
			horizon = &last
		}
	}
	var out []overlayListEntry
	i, j, full := 0, 0, false
	for i < len(branch) || j < len(source) {
		var e overlayListEntry
		if j >= len(source) || i < len(branch) && branch[i].key <= source[j].key {
			e = branch[i]
			if j < len(source) && source[j].key == e.key {
				j++
			}
			i++
		} else {
			e = source[j]
			j++
			if !e.prefix && hidden[e.key] {
				continue
			}
		}
		if horizon != nil && e.key > horizon.key {
			break
		}
		if len(out) == int(list.Limit) {
			full = true
			break
		}
		out = append(out, e)
	}
	page := objectstorage.ObjectPage{}
	for _, e := range out {
		if e.prefix {
			page.CommonPrefixes = append(page.CommonPrefixes, e.key)
		} else {
			page.Items = append(page.Items, e.object)
		}
	}
	// A full page resumes after its last entry; a page cut at the horizon has
	// considered every entry up to it, emitted or tombstoned.
	switch {
	case full:
		page.NextCursor = encodeOverlayListCursor(out[len(out)-1].after())
	case horizon != nil:
		page.NextCursor = encodeOverlayListCursor(horizon.after())
	}
	return page, nil
}

// errOverlayListAnswered means the listing already wrote its error response.
var errOverlayListAnswered = errors.New("overlay listing answered")
