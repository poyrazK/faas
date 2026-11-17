package s3gateway

import (
	"context"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// Point-in-time overlays (ADR-20261031193635 amendment 2026-11-14). A branch
// bucket whose source had versioning on when the branch was made reads the
// source as of the branch point: the version that was current then. A key
// with no such version (written later, deleted then, or its version since
// removed by a lifecycle rule) reads as missing, so a branch never serves
// data newer than its branch point.

const (
	// asOfVersionPage is one provider page of versions.
	asOfVersionPage = 1000
	// asOfMaxPages bounds the provider pages one request may read: a key
	// with more versions than that, or a listing whose keys all postdate
	// the branch point for that long, answers unavailable.
	asOfMaxPages = 8
)

// asOfCandidate is the best version of one key seen so far.
type asOfCandidate struct {
	version objectstorage.ListedObjectVersion
	found   bool
}

// consider keeps v when it was current at at and is newer than the best so
// far. Of two versions written in the same instant the one the provider
// marks latest wins, and otherwise a delete marker, so a tie never revives
// a deleted key.
func (c *asOfCandidate) consider(v objectstorage.ListedObjectVersion, at time.Time) {
	if v.LastModified.After(at) {
		return
	}
	if c.found {
		switch {
		case v.LastModified.Before(c.version.LastModified):
			return
		case v.LastModified.Equal(c.version.LastModified) && !v.IsLatest && (c.version.IsLatest || c.version.DeleteMarker):
			return
		}
	}
	c.version, c.found = v, true
}

// live reports whether the key held data at the branch point.
func (c asOfCandidate) live() bool { return c.found && !c.version.DeleteMarker }

// asOfVersion returns the version of key current at at, and false when the
// key held no data then. countRequest is called before each provider request
// and stops the lookup when it returns false.
func asOfVersion(ctx context.Context, lister objectstorage.ObjectVersionLister, bucket, key string, at time.Time, countRequest func() bool) (objectstorage.ListedObjectVersion, bool, error) {
	var best asOfCandidate
	request := objectstorage.ObjectVersionListRequest{Prefix: key, Limit: asOfVersionPage}
	for range asOfMaxPages {
		if !countRequest() {
			return objectstorage.ListedObjectVersion{}, false, errOverlayListAnswered
		}
		page, err := lister.ListObjectVersionPage(ctx, bucket, request)
		if err != nil {
			return objectstorage.ListedObjectVersion{}, false, err
		}
		for _, v := range page.Items {
			if v.Key == key {
				best.consider(v, at)
			}
		}
		// Keys sort after their prefixes, so once a page stops past key
		// every version of key has been seen.
		if page.NextKeyMarker == "" || page.NextKeyMarker > key {
			return best.version, best.live(), nil
		}
		request.KeyMarker, request.ProviderVersionMarker = page.NextKeyMarker, page.NextProviderVersionMarker
	}
	return objectstorage.ListedObjectVersion{}, false, objectstorage.ErrUnavailable
}

// asOfListing is one page of a source listed as of the branch point.
type asOfListing struct {
	entries []overlayListEntry
	// horizon is set when more keys follow. It is a start-after boundary:
	// every key up to it has been decided, listed or not.
	horizon string
}

// asOfElement is a provider version or common prefix in key order.
type asOfElement struct {
	key     string
	prefix  bool
	version objectstorage.ListedObjectVersion
}

func asOfElements(page objectstorage.ObjectVersionListPage) []asOfElement {
	out := make([]asOfElement, 0, len(page.Items)+len(page.CommonPrefixes))
	for _, v := range page.Items {
		out = append(out, asOfElement{key: v.Key, version: v})
	}
	for _, p := range page.CommonPrefixes {
		out = append(out, asOfElement{key: p, prefix: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// listAsOf lists the source as of at, in the order and shape of a plain
// listing. A key is decided only once all its versions are in: a provider
// page stops at its next marker, whose key may continue on the next page.
// A common prefix lists when the provider reports one, even if it holds
// only keys written after the branch point; listOverlayObjects checks it
// before the branch sees it (overlayPrefixVisible).
func listAsOf(ctx context.Context, lister objectstorage.ObjectVersionLister, bucket string, list objectstorage.ObjectListRequest, at time.Time, countRequest func() bool) (asOfListing, error) {
	request := objectstorage.ObjectVersionListRequest{Prefix: list.Prefix, Delimiter: list.Delimiter, KeyMarker: list.StartAfter, Limit: asOfVersionPage}
	var out asOfListing
	var pending string
	var best asOfCandidate
	decided := "" // start-after boundary past the last decided element
	finish := func() {
		if pending == "" {
			return
		}
		if best.live() {
			object := best.version.Object
			out.entries = append(out.entries, overlayListEntry{key: pending, object: object})
		}
		decided = max(decided, pending)
		pending, best = "", asOfCandidate{}
	}
	for range asOfMaxPages {
		if !countRequest() {
			return asOfListing{}, errOverlayListAnswered
		}
		page, err := lister.ListObjectVersionPage(ctx, bucket, request)
		if err != nil {
			return asOfListing{}, err
		}
		for _, e := range asOfElements(page) {
			if e.prefix {
				finish()
				out.entries = append(out.entries, overlayListEntry{key: e.key, prefix: true})
				decided = max(decided, e.key+string(utf8.MaxRune))
				continue
			}
			if e.key != pending {
				finish()
				pending = e.key
			}
			best.consider(e.version, at)
		}
		if page.NextKeyMarker == "" {
			finish()
			return out, nil
		}
		// The page stopped at its next marker: a key there may have more
		// versions on the next page; any other key is complete.
		if pending != page.NextKeyMarker {
			finish()
		}
		request.KeyMarker, request.ProviderVersionMarker = page.NextKeyMarker, page.NextProviderVersionMarker
		if len(out.entries) >= int(list.Limit) && decided != "" {
			out.horizon = decided
			return out, nil
		}
	}
	if decided == "" || decided <= list.StartAfter {
		return asOfListing{}, objectstorage.ErrUnavailable
	}
	out.horizon = decided
	return out, nil
}
