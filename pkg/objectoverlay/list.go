package objectoverlay

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// PrefixProbes bounds the source common prefixes one merged page checks
	// for a visible key. A page that reaches it ends there with a
	// continuation, as a page cut at a provider horizon does.
	PrefixProbes = 16
	// prefixProbePage is the source listing one prefix check reads.
	prefixProbePage = 1000
)

// Entry is one key or common prefix of a listing page, in the single
// lexicographic order S3 lists them in. FromSource marks an entry the
// branch reads from its source bucket.
type Entry struct {
	Key        string
	Prefix     bool
	Object     objectstorage.Object
	FromSource bool
}

// After is the start-after boundary past this entry. Past a common prefix
// it is the largest key the prefix can hold, so the next page does not roll
// the same prefix up again.
func (e Entry) After() string {
	if e.Prefix {
		return e.Key + string(utf8.MaxRune)
	}
	return e.Key
}

// Entries merges a provider page's objects and common prefixes in key order.
func Entries(page objectstorage.ObjectPage, fromSource bool) []Entry {
	out := make([]Entry, 0, len(page.Items)+len(page.CommonPrefixes))
	i, j := 0, 0
	for i < len(page.Items) || j < len(page.CommonPrefixes) {
		if j >= len(page.CommonPrefixes) || i < len(page.Items) && page.Items[i].Key < page.CommonPrefixes[j] {
			out = append(out, Entry{Key: page.Items[i].Key, Object: page.Items[i], FromSource: fromSource})
			i++
			continue
		}
		out = append(out, Entry{Key: page.CommonPrefixes[j], Prefix: true, FromSource: fromSource})
		j++
	}
	return out
}

// Lister lists one bucket the way ListObjectsV2 does: from StartAfter,
// rolled up at Delimiter. Cursor is never set. It counts its own provider
// request.
type Lister func(ctx context.Context, req objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error)

// ProviderLister lists bucket on p from a start-after boundary, counting
// each provider request with count. A provider without start-after listing
// is unsupported: a merged page cannot resume from a provider cursor.
func ProviderLister(p objectstorage.Provider, bucket string, count Counter) Lister {
	return func(ctx context.Context, req objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
		lister, ok := p.(objectstorage.ObjectV2Lister)
		if !ok {
			return objectstorage.ObjectPage{}, objectstorage.ErrUnsupported
		}
		if err := count(); err != nil {
			return objectstorage.ObjectPage{}, err
		}
		return lister.ListObjectsV2(ctx, bucket, req)
	}
}

// Source is the bucket a branch reads through to: its current objects
// (List), or, when At is set, its objects as of the branch point read from
// Versions in Bucket, counting each provider page with Count.
type Source struct {
	List     Lister
	Versions objectstorage.ObjectVersionLister
	Bucket   string
	At       time.Time
	Count    Counter
}

// entries lists the source side of a merged page. horizon is set when the
// source has more: no source entry past it was read.
func (s Source) entries(ctx context.Context, req objectstorage.ObjectListRequest) ([]Entry, *Entry, error) {
	if s.At.IsZero() {
		page, err := s.List(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		entries := Entries(page, true)
		if page.NextCursor == "" {
			return entries, nil, nil
		}
		if len(entries) == 0 {
			return nil, nil, objectstorage.ErrUnavailable
		}
		last := entries[len(entries)-1]
		return entries, &last, nil
	}
	entries, horizon, err := listAsOf(ctx, s.Versions, s.Bucket, req, s.At, s.Count)
	if err != nil || horizon == "" {
		return entries, nil, err
	}
	// The horizon is already a start-after boundary: a plain key entry's
	// After is its key.
	return entries, &Entry{Key: horizon}, nil
}

// Tombstones looks up the keys a branch deleted.
type Tombstones interface {
	ObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) (map[string]bool, error)
}

// Listing is one merged page to list.
type Listing struct {
	Branch Lister
	Source Source
	// Store holds the branch's tombstones. When it also implements
	// state.ObjectBucketOverlayTombstonePrefixStore, a live source prefix
	// with no tombstone under it lists without a check.
	Store    Tombstones
	BucketID string
	// Request is the page asked for. Its Cursor is ignored: a merged page
	// resumes from StartAfter.
	Request objectstorage.ObjectListRequest
}

// Page is one merged page. Next, when set, is the start-after boundary the
// next page starts from.
type Page struct {
	Entries []Entry
	Next    string
}

// List lists one merged page of a branch bucket and its source. Both lists
// start after the same boundary; the branch's entry wins a key both hold, a
// tombstoned source key is skipped, and the page stops at the first
// truncated list's last entry so that no key is skipped. A source common
// prefix the branch does not hold lists only when it holds a key the branch
// reads (prefixVisible); a page that has checked PrefixProbes of them ends
// before the next.
func List(ctx context.Context, l Listing) (Page, error) {
	req := l.Request
	req.Cursor = ""
	branchPage, err := l.Branch(ctx, req)
	if err != nil {
		return Page{}, err
	}
	branch := Entries(branchPage, false)
	source, sourceHorizon, err := l.Source.entries(ctx, req)
	if err != nil {
		return Page{}, err
	}
	var keys []string
	for _, e := range source {
		if !e.Prefix {
			keys = append(keys, e.Key)
		}
	}
	hidden, err := tombstones(ctx, l.Store, l.BucketID, keys)
	if err != nil {
		return Page{}, objectstorage.ErrUnavailable
	}
	var horizon *Entry
	if branchPage.NextCursor != "" {
		if len(branch) == 0 {
			return Page{}, objectstorage.ErrUnavailable
		}
		last := branch[len(branch)-1]
		horizon = &last
	}
	if sourceHorizon != nil && (horizon == nil || sourceHorizon.Key < horizon.Key) {
		horizon = sourceHorizon
	}
	var out []Entry
	var last *Entry // the last entry decided, listed or not
	i, j, full, cut, probes := 0, 0, false, false, 0
	for i < len(branch) || j < len(source) {
		var e Entry
		if j >= len(source) || i < len(branch) && branch[i].Key <= source[j].Key {
			e = branch[i]
			if j < len(source) && source[j].Key == e.Key {
				j++
			}
			i++
		} else {
			e = source[j]
			j++
			if !e.Prefix && hidden[e.Key] {
				last = &e
				continue
			}
		}
		if horizon != nil && e.Key > horizon.Key {
			break
		}
		if len(out) == int(req.Limit) {
			full = true
			break
		}
		if e.FromSource && e.Prefix {
			if probes == PrefixProbes {
				cut = true
				break
			}
			probes++
			visible, err := l.prefixVisible(ctx, e.Key)
			if err != nil {
				return Page{}, err
			}
			if !visible {
				last = &e
				continue
			}
		}
		out = append(out, e)
		last = &e
	}
	page := Page{Entries: out}
	switch {
	case full:
		page.Next = out[len(out)-1].After()
	case cut:
		// Every entry up to last is decided; the next page starts after it.
		page.Next = last.After()
	case horizon != nil:
		// A page cut at the horizon has considered every entry up to it,
		// listed or tombstoned.
		page.Next = horizon.After()
	}
	return page, nil
}

// prefixVisible reports whether a source common prefix, which the branch
// does not list itself, holds a key the branch reads (ADR-20261031193635
// amendment 2026-11-17). Under live_parent a prefix with no tombstone under
// it always does: the source listed it, so it holds a current object.
// Otherwise one source listing of the prefix decides: visible when it holds
// a key that is not tombstoned (for as_of, a key live at the branch point).
// A prefix the listing cannot decide within one page lists.
func (l Listing) prefixVisible(ctx context.Context, prefix string) (bool, error) {
	if l.Source.At.IsZero() {
		if store, ok := l.Store.(state.ObjectBucketOverlayTombstonePrefixStore); ok {
			deleted, err := store.ObjectBucketOverlayTombstonesUnder(ctx, l.BucketID, prefix)
			if err != nil {
				return false, objectstorage.ErrUnavailable
			}
			if !deleted {
				return true, nil
			}
		}
	}
	entries, horizon, err := l.Source.entries(ctx, objectstorage.ObjectListRequest{Prefix: prefix, Limit: prefixProbePage})
	if err != nil {
		return false, err
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, e.Key)
	}
	hidden, err := tombstones(ctx, l.Store, l.BucketID, keys)
	if err != nil {
		return false, objectstorage.ErrUnavailable
	}
	for _, key := range keys {
		if !hidden[key] {
			return true, nil
		}
	}
	return horizon != nil, nil
}

// tombstones reports which keys the branch deleted, in lookups the store
// accepts: a source listing may hold more keys than one lookup takes (an
// as_of listing reads whole provider pages past its limit).
func tombstones(ctx context.Context, store Tombstones, bucketID string, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(keys); start += state.ObjectBucketOverlayTombstoneBatch {
		batch := keys[start:min(start+state.ObjectBucketOverlayTombstoneBatch, len(keys))]
		hidden, err := store.ObjectBucketOverlayTombstones(ctx, bucketID, batch)
		if err != nil {
			return nil, err
		}
		for key := range hidden {
			out[key] = true
		}
	}
	return out, nil
}

// Tombstoned reports which keys the branch deleted, batching the lookups.
func Tombstoned(ctx context.Context, store Tombstones, bucketID string, keys []string) (map[string]bool, error) {
	return tombstones(ctx, store, bucketID, keys)
}
