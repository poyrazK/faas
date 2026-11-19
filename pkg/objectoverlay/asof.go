// Package objectoverlay reads an environment branch's bucket over its
// source bucket (ADR-20261031193635 section 5): the branch's own objects win,
// keys the branch deleted stay hidden, and every other key comes from the
// source, read live or as of the branch point. The S3 gateway serves data
// through it; apid's object API lists with it.
package objectoverlay

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
	// AsOfVersionPage is one provider page of versions.
	AsOfVersionPage = 1000
	// AsOfMaxPages bounds the provider pages one request may read: a key
	// with more versions than that, or a listing whose keys all postdate
	// the branch point for that long, answers unavailable.
	AsOfMaxPages = 8
)

// Counter is called before each provider request. An error stops the read
// and is returned unchanged, so a caller can recognise its own.
type Counter func() error

// candidate is the best version of one key seen so far.
type candidate struct {
	version objectstorage.ListedObjectVersion
	found   bool
}

// consider keeps v when it was current at at and is newer than the best so
// far. Of two versions written in the same instant the one the provider
// marks latest wins, and otherwise a delete marker, so a tie never revives
// a deleted key.
func (c *candidate) consider(v objectstorage.ListedObjectVersion, at time.Time) {
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
func (c candidate) live() bool { return c.found && !c.version.DeleteMarker }

// AsOfVersion returns the version of key current at at, and false when the
// key held no data then.
func AsOfVersion(ctx context.Context, lister objectstorage.ObjectVersionLister, bucket, key string, at time.Time, count Counter) (objectstorage.ListedObjectVersion, bool, error) {
	var best candidate
	request := objectstorage.ObjectVersionListRequest{Prefix: key, Limit: AsOfVersionPage}
	for range AsOfMaxPages {
		if err := count(); err != nil {
			return objectstorage.ListedObjectVersion{}, false, err
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

// element is a provider version or common prefix in key order.
type element struct {
	key     string
	prefix  bool
	version objectstorage.ListedObjectVersion
}

func elements(page objectstorage.ObjectVersionListPage) []element {
	out := make([]element, 0, len(page.Items)+len(page.CommonPrefixes))
	for _, v := range page.Items {
		out = append(out, element{key: v.Key, version: v})
	}
	for _, p := range page.CommonPrefixes {
		out = append(out, element{key: p, prefix: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// listAsOf lists the source as of at, in the order and shape of a plain
// listing. A key is decided only once all its versions are in: a provider
// page stops at its next marker, whose key may continue on the next page.
// A common prefix lists when the provider reports one, even if it holds only
// keys written after the branch point; List checks it before the branch
// sees it. horizon, when set, is the start-after boundary past every decided
// element: more follow it.
func listAsOf(ctx context.Context, lister objectstorage.ObjectVersionLister, bucket string, list objectstorage.ObjectListRequest, at time.Time, count Counter) (entries []Entry, horizon string, err error) {
	request := objectstorage.ObjectVersionListRequest{Prefix: list.Prefix, Delimiter: list.Delimiter, KeyMarker: list.StartAfter, Limit: AsOfVersionPage}
	var pending string
	var best candidate
	decided := "" // start-after boundary past the last decided element
	finish := func() {
		if pending == "" {
			return
		}
		if best.live() {
			entries = append(entries, Entry{Key: pending, Object: best.version.Object, FromSource: true})
		}
		decided = max(decided, pending)
		pending, best = "", candidate{}
	}
	for range AsOfMaxPages {
		if err := count(); err != nil {
			return nil, "", err
		}
		page, err := lister.ListObjectVersionPage(ctx, bucket, request)
		if err != nil {
			return nil, "", err
		}
		for _, e := range elements(page) {
			if e.prefix {
				finish()
				entries = append(entries, Entry{Key: e.key, Prefix: true, FromSource: true})
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
			return entries, "", nil
		}
		// The page stopped at its next marker: a key there may have more
		// versions on the next page; any other key is complete.
		if pending != page.NextKeyMarker {
			finish()
		}
		request.KeyMarker, request.ProviderVersionMarker = page.NextKeyMarker, page.NextProviderVersionMarker
		if len(entries) >= int(list.Limit) && decided != "" {
			return entries, decided, nil
		}
	}
	if decided == "" || decided <= list.StartAfter {
		return nil, "", objectstorage.ErrUnavailable
	}
	return entries, decided, nil
}
