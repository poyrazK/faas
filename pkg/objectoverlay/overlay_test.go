package objectoverlay

// adr: 20261031193635

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

var branchPoint = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func TestCandidateTies(t *testing.T) {
	at := branchPoint
	version := func(id string, offset time.Duration, latest, marker bool) objectstorage.ListedObjectVersion {
		return objectstorage.ListedObjectVersion{Object: objectstorage.Object{Key: "k", LastModified: at.Add(offset)}, ProviderVersionID: id, IsLatest: latest, DeleteMarker: marker}
	}
	for _, tc := range []struct {
		name     string
		versions []objectstorage.ListedObjectVersion
		want     string
		live     bool
	}{
		{"newest before the branch point", []objectstorage.ListedObjectVersion{version("new", time.Hour, true, false), version("mid", -time.Hour, false, false), version("old", -2*time.Hour, false, false)}, "mid", true},
		{"exactly at the branch point", []objectstorage.ListedObjectVersion{version("at", 0, true, false)}, "at", true},
		{"only later versions", []objectstorage.ListedObjectVersion{version("new", time.Second, true, false)}, "", false},
		{"deleted then", []objectstorage.ListedObjectVersion{version("gone", -time.Hour, false, true), version("old", -2*time.Hour, false, false)}, "gone", false},
		{"tie: latest wins", []objectstorage.ListedObjectVersion{version("x", -time.Hour, false, false), version("y", -time.Hour, true, false)}, "y", true},
		{"tie: marker beats data", []objectstorage.ListedObjectVersion{version("x", -time.Hour, false, false), version("m", -time.Hour, false, true)}, "m", false},
		{"tie: data never beats marker", []objectstorage.ListedObjectVersion{version("m", -time.Hour, false, true), version("x", -time.Hour, false, false)}, "m", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c candidate
			for _, v := range tc.versions {
				c.consider(v, at)
			}
			if c.version.ProviderVersionID != tc.want || c.live() != tc.live {
				t.Fatalf("candidate = %q live=%v, want %q live=%v", c.version.ProviderVersionID, c.live(), tc.want, tc.live)
			}
		})
	}
}

// batchTombstoneStore refuses a lookup larger than the store's batch, as the
// real stores do, and records each lookup's size.
type batchTombstoneStore struct {
	deleted map[string]bool
	batches []int
}

func (s *batchTombstoneStore) ObjectBucketOverlayTombstones(_ context.Context, _ string, keys []string) (map[string]bool, error) {
	if len(keys) > state.ObjectBucketOverlayTombstoneBatch {
		return nil, state.ErrInvalidArgument
	}
	s.batches = append(s.batches, len(keys))
	out := map[string]bool{}
	for _, key := range keys {
		if s.deleted[key] {
			out[key] = true
		}
	}
	return out, nil
}

// TestTombstonedLooksUpInBatches covers an as_of listing, which reads whole
// provider pages and so can hold more keys than one lookup takes.
func TestTombstonedLooksUpInBatches(t *testing.T) {
	keys := make([]string, 2*state.ObjectBucketOverlayTombstoneBatch+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%05d", i)
	}
	last := keys[len(keys)-1]
	store := &batchTombstoneStore{deleted: map[string]bool{keys[0]: true, last: true}}
	hidden, err := Tombstoned(t.Context(), store, "bucket", keys)
	if err != nil || len(hidden) != 2 || !hidden[keys[0]] || !hidden[last] {
		t.Fatalf("hidden = %v, %v", hidden, err)
	}
	if !slices.Equal(store.batches, []int{1000, 1000, 1}) {
		t.Fatalf("lookups = %v", store.batches)
	}
}

// memLister lists a key set the way ListObjectsV2 does, pageSize at a time.
func memLister(keys []string, pageSize int) Lister {
	sorted := slices.Sorted(slices.Values(keys))
	return func(_ context.Context, req objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
		var page objectstorage.ObjectPage
		limit := min(int(req.Limit), pageSize)
		for _, key := range sorted {
			if key <= req.StartAfter || !strings.HasPrefix(key, req.Prefix) {
				continue
			}
			if len(page.Items) == limit {
				page.NextCursor = "more"
				break
			}
			page.Items = append(page.Items, objectstorage.Object{Key: key})
		}
		return page, nil
	}
}

// TestListMarksWhereEachKeyComesFrom pages a live merge and checks each
// key's origin: the branch wins a key both hold, a tombstoned source key is
// hidden.
func TestListMarksWhereEachKeyComesFrom(t *testing.T) {
	store := &batchTombstoneStore{deleted: map[string]bool{"gone": true}}
	for _, pageSize := range []int{1, 2, 1000} {
		for _, limit := range []int32{1, 3, 1000} {
			l := Listing{
				Branch:   memLister([]string{"b", "own"}, pageSize),
				Source:   Source{List: memLister([]string{"a", "b", "gone", "z"}, pageSize)},
				Store:    store,
				BucketID: "bucket",
			}
			got := map[string]bool{}
			after := ""
			for range 20 {
				l.Request = objectstorage.ObjectListRequest{StartAfter: after, Limit: limit}
				page, err := List(t.Context(), l)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range page.Entries {
					got[e.Key] = e.FromSource
				}
				if page.Next == "" {
					break
				}
				after = page.Next
			}
			keys := slices.Collect(func(yield func(string) bool) {
				for k := range got {
					if !yield(k) {
						return
					}
				}
			})
			sort.Strings(keys)
			if !slices.Equal(keys, []string{"a", "b", "own", "z"}) || !got["a"] || got["b"] || got["own"] || !got["z"] {
				t.Fatalf("page size %d limit %d: origins = %v", pageSize, limit, got)
			}
		}
	}
}
