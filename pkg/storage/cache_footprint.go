package storage

// Physical cache footprint — ADR-633.
//
// A snapshot's private drive is a copy-on-write clone of its app layer: the
// guest writes a few MiB and every other block is still the layer's. The
// cache budget used to sum each file's allocated blocks, which counts a block
// shared by N files N times, so sharing saved disk without letting the cache
// keep a single extra snapshot. The footprint below counts every physical
// extent held by cache entries once: a file's unshared extents belong to it
// alone, and shared extents are merged across entries before they are summed.

import (
	"errors"
	"sort"
)

// physicalExtent is one allocated range of a file on its block device.
type physicalExtent struct {
	physical int64
	length   int64
	// shared reports that another file references the same blocks.
	shared bool
}

// errExtentsUnsupported reports a platform or filesystem without FIEMAP.
var errExtentsUnsupported = errors.New("storage: physical extents unsupported")

// readExtents is the platform extent reader; tests replace it.
var readExtents = fileExtents

type interval struct{ start, end int64 }

type footprintEntry struct {
	exclusive int64
	shared    []interval
	alive     bool
}

// cacheFootprint tracks the physical bytes held by a set of cache entries.
type cacheFootprint struct {
	entries []footprintEntry
	// sharedTotal is the merged size of every live entry's shared ranges;
	// dirty marks it for recomputation after an eviction.
	sharedTotal int64
	dirty       bool
}

// newCacheFootprint measures entries. An entry whose extents cannot be read
// counts its allocated size as its own, which is the pre-ADR-633 accounting.
func newCacheFootprint(entries []cacheEntry) *cacheFootprint {
	f := &cacheFootprint{entries: make([]footprintEntry, len(entries)), dirty: true}
	for i, e := range entries {
		f.entries[i] = footprintEntry{exclusive: e.size, alive: true}
		extents, err := readExtents(e.path)
		if err != nil {
			continue
		}
		var exclusive int64
		var shared []interval
		for _, x := range extents {
			if x.length <= 0 {
				continue
			}
			if x.shared {
				shared = append(shared, interval{x.physical, x.physical + x.length})
			} else {
				exclusive += x.length
			}
		}
		f.entries[i] = footprintEntry{exclusive: exclusive, shared: shared, alive: true}
	}
	return f
}

// total returns the physical bytes held by live entries.
func (f *cacheFootprint) total() int64 {
	var exclusive int64
	for _, e := range f.entries {
		if e.alive {
			exclusive += e.exclusive
		}
	}
	if f.dirty {
		f.sharedTotal = f.mergedShared()
		f.dirty = false
	}
	return exclusive + f.sharedTotal
}

// drop removes entry i from the footprint after it has been evicted.
func (f *cacheFootprint) drop(i int) {
	if i < 0 || i >= len(f.entries) || !f.entries[i].alive {
		return
	}
	f.entries[i].alive = false
	if len(f.entries[i].shared) > 0 {
		f.dirty = true
	}
}

func (f *cacheFootprint) mergedShared() int64 {
	var all []interval
	for _, e := range f.entries {
		if e.alive {
			all = append(all, e.shared...)
		}
	}
	if len(all) == 0 {
		return 0
	}
	sort.Slice(all, func(i, j int) bool { return all[i].start < all[j].start })
	var total int64
	cur := all[0]
	for _, next := range all[1:] {
		if next.start <= cur.end {
			cur.end = max(cur.end, next.end)
			continue
		}
		total += cur.end - cur.start
		cur = next
	}
	return total + cur.end - cur.start
}

// ExclusiveBytes reports the bytes of path that no other file shares. For a
// writable drive cloned from its app layer this is what the guest wrote. ok
// is false when the platform cannot tell shared blocks apart.
func ExclusiveBytes(path string) (bytes int64, ok bool) {
	extents, err := readExtents(path)
	if err != nil {
		return 0, false
	}
	for _, x := range extents {
		if !x.shared && x.length > 0 {
			bytes += x.length
		}
	}
	return bytes, true
}
