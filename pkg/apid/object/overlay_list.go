package object

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/objectoverlay"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// overlayCursorPrefix marks apid's own cursor for a branch bucket's merged
// listing: the start-after boundary of the next page.
const overlayCursorPrefix = "gregale-overlay-1."

// listOverlayBucketObjects lists an environment branch's bucket as the S3
// gateway serves it (ADR-20261031193635 amendment 2026-11-17): the branch's
// own objects over its parent's, keys the branch deleted hidden, the parent
// read live or as of the branch point, each parent object marked
// from_parent. handled is false for a bucket that overlays nothing apid can
// read, which then lists as an ordinary bucket. Every provider request is
// counted against the branch bucket, as the gateway counts a read-through.
func (s *Service) listOverlayBucketObjects(ctx context.Context, b state.ObjectBucket, provider objectstorage.Provider, prefix, cursor string, limit int32) (objectstorage.ObjectPage, bool, error) {
	overlays, ok := s.Host.Store().(state.ObjectBucketOverlayStore)
	if !ok {
		return objectstorage.ObjectPage{}, false, nil
	}
	overlay, err := overlays.ObjectBucketOverlay(ctx, b.ID)
	if errors.Is(err, state.ErrNotFound) {
		return objectstorage.ObjectPage{}, false, nil
	}
	if err != nil {
		return objectstorage.ObjectPage{}, true, objectstorage.ErrUnavailable
	}
	src := overlay.Source
	if src.ID == "" || src.State != "ready" || src.AccountID != b.AccountID ||
		overlay.Consistency != state.ObjectBucketOverlayLiveParent && overlay.Consistency != state.ObjectBucketOverlayAsOf {
		return objectstorage.ObjectPage{}, false, nil
	}
	after := ""
	if cursor != "" {
		decoded, ok := decodeOverlayCursor(cursor)
		if !ok {
			return objectstorage.ObjectPage{}, true, objectstorage.ErrInvalid
		}
		after = decoded
	}
	backend, err := s.Host.ObjectStorage().Resolve(src.BackendID, src.BackendFingerprint)
	if err != nil {
		return objectstorage.ObjectPage{}, true, objectstorage.ErrUnavailable
	}
	count := func() error { return s.CustomerObjectRequestRecorder(b)(ctx) }
	source := objectoverlay.Source{List: objectoverlay.ProviderLister(backend.Provider, src.PhysicalName, count), Bucket: src.PhysicalName, Count: count}
	if overlay.Consistency == state.ObjectBucketOverlayAsOf {
		versions, ok := backend.Provider.(objectstorage.ObjectVersionLister)
		if !ok || overlay.AsOf.IsZero() {
			// Never list newer data than the branch point.
			return objectstorage.ObjectPage{}, true, objectstorage.ErrUnavailable
		}
		source.Versions, source.At = versions, overlay.AsOf
	}
	merged, err := objectoverlay.List(ctx, objectoverlay.Listing{
		Branch: objectoverlay.ProviderLister(provider, b.PhysicalName, count), Source: source,
		Store: overlays, BucketID: b.ID,
		Request: objectstorage.ObjectListRequest{Prefix: prefix, StartAfter: after, Limit: limit},
	})
	if err != nil {
		return objectstorage.ObjectPage{}, true, err
	}
	page := objectstorage.ObjectPage{Items: make([]objectstorage.Object, 0, len(merged.Entries))}
	for _, e := range merged.Entries {
		item := e.Object
		item.FromParent = e.FromSource
		page.Items = append(page.Items, item)
	}
	if merged.Next != "" {
		page.NextCursor = overlayCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte(merged.Next))
	}
	return page, true, nil
}

func decodeOverlayCursor(cursor string) (string, bool) {
	encoded, ok := strings.CutPrefix(cursor, overlayCursorPrefix)
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > 1024+utf8.UTFMax || !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
}
