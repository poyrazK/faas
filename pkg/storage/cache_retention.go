package storage

// adr: 20261114120000
//
// Node cache retention. The cache budget now comes from the disk, so the
// budget rarely forces an eviction; what the node no longer needs has to be
// removed on purpose (snapshothipd's sweep), and when the disk does fill, the
// entries nothing needs go first and the snapshots of live deployments last.

import (
	"time"
)

// Eviction tiers order what enforceBudgetLocked evicts first. Within a tier
// the oldest entry goes first.
const (
	// EvictFirst marks an entry nothing references any more: a capture of a
	// deleted or stale snapshot, a runtime release no deployment uses.
	EvictFirst = 0
	// EvictNormal is every other entry (app layers, bases, kernels).
	EvictNormal = 1
	// EvictLast marks the snapshot captures of live deployments: evicting
	// one turns the app's next wake into a remote fetch of its drive and
	// memory (2026-11-13, a 6.2 s wake and a 503 on production-us).
	EvictLast = 2
)

// CachedEntry is one blob in the node cache.
type CachedEntry struct {
	Key            string
	ModTime        time.Time
	AllocatedBytes int64
}

// setMaxBytes replaces the byte budget; wrapWithCache sets it once the cache
// directory exists and its filesystem can be measured.
func (c *LocalCacheBackend) setMaxBytes(n int64) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxBytes = n
}

// MaxBytes reports the byte budget.
func (c *LocalCacheBackend) MaxBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxBytes
}

// SetEvictionTier makes eviction take entries in tier order (EvictFirst,
// EvictNormal, EvictLast), oldest first within a tier. nil restores plain
// oldest-first eviction.
func (c *LocalCacheBackend) SetEvictionTier(tier func(key string) int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictionTier = tier
}

// tierLocked is the eviction tier of key. Caller holds c.mu.
func (c *LocalCacheBackend) tierLocked(key string) int {
	if c.evictionTier == nil {
		return EvictNormal
	}
	return c.evictionTier(key)
}

// LocalEntries lists the blobs this node holds in its cache, without asking
// the parent: what is on this disk, not what shared storage has.
func (c *LocalCacheBackend) LocalEntries() ([]CachedEntry, error) {
	c.mu.Lock()
	entries, err := c.snapshotCacheLocked()
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]CachedEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, CachedEntry{Key: e.key, ModTime: e.modTime, AllocatedBytes: e.size})
	}
	return out, nil
}

// EvictLocal removes key from this node's cache only; the parent keeps its
// copy, so a later read fetches it again.
func (c *LocalCacheBackend) EvictLocal(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	c.evictCache(key)
	return nil
}
