package storage

// ResolveCacheMaxBytes picks the artifact-cache byte budget for this host.
//
// The budget was a flat 8 GiB constant, which does not scale with the machine
// it runs on: that is 12 % of the 64 GB reference node and 50 % of the 16 GB
// n2-standard-4 nodes the fleet actually runs. Deriving it from MemTotal makes
// the relationship explicit and lets a larger node use its RAM instead of
// leaving most of it unexploited.
//
// The fraction is deliberately chosen so the current fleet is unchanged:
// 50 % of a 16 GiB node is the 8 GiB it already gets. This is NOT an attempt
// to free page cache by shrinking the cache — that would likely backfire.
// A cache miss costs a remote registry fetch (3.8 s measured on a 2 GiB
// layer), while a cold page-cache hit costs roughly 0.85 s, so trading cache
// capacity for page-cache headroom makes the tail worse, not better. Sizing
// the cache against tenant memory is a real trade that belongs in the spec
// §13 RAM budget with an ADR, not in a default.
//
// Bounds keep pathological hosts sane: never below 2 GiB (a single scale-plan
// app layer plus its snapshot must fit or the cache thrashes on one app), and
// never above 32 GiB (beyond that the eviction walk, which stats every entry,
// costs more than the hit rate it buys).
func ResolveCacheMaxBytes(memTotalBytes int64) int64 {
	if memTotalBytes <= 0 {
		return DefaultCacheMaxBytes
	}
	budget := memTotalBytes / 2
	if budget < minCacheMaxBytes {
		return minCacheMaxBytes
	}
	if budget > maxCacheMaxBytes {
		return maxCacheMaxBytes
	}
	return budget
}

const (
	// minCacheMaxBytes must hold at least one scale-plan app layer (2 GiB
	// logical, far less on disk) plus its snapshot.
	minCacheMaxBytes int64 = 2 << 30
	// maxCacheMaxBytes bounds enforceBudgetLocked, which stats every entry
	// in the cache on each write.
	maxCacheMaxBytes int64 = 32 << 30
)
