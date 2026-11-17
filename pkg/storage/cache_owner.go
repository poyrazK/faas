package storage

// adr: 20261114182830
//
// One process evicts a shared node cache. vmmd, schedd, imaged and builderd
// open the same cache directory on a compute node, but only vmmd knows which
// entries live deployments need (snapshothipd sets the eviction tiers). The
// others evicted oldest-first across everything: under a 48 GiB cap on
// production-us (2026-11-16/17) they deleted live app layers, and snapshot
// fan-out retired ~80 replicas for it. vmmd now holds an advisory lock on
// cacheEvictionOwnerFile while it runs; another process using the directory
// then leaves snapshot captures and app layers alone, and vmmd enforces the
// budget itself on every node cache sweep. Without vmmd (a control-plane
// node), every process evicts as before. The kernel drops the lock when vmmd
// exits, so a crashed owner never blocks eviction.

import "strings"

// cacheEvictionOwnerFile sits in the cache root, outside the two-character
// buckets the cache walk reads.
const cacheEvictionOwnerFile = ".eviction-owner"

// ownerProtectedKey reports whether a process that does not own eviction must
// leave key alone: a snapshot capture or an app layer, whose liveness only the
// owner knows.
func ownerProtectedKey(key string) bool {
	return strings.HasPrefix(key, "snap/") || strings.HasPrefix(key, "apps/") || strings.HasPrefix(key, "layers/")
}

// ClaimEvictionOwnership makes this process the cache's eviction owner until
// release is called or the process exits. vmmd calls it at start.
func (c *LocalCacheBackend) ClaimEvictionOwnership() (func(), error) {
	unlock, err := lockEvictionOwner(c.root)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.evictionOwner = true
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.evictionOwner = false
		c.mu.Unlock()
		unlock()
	}, nil
}

// ClaimCacheEvictionOwnership claims eviction of the cache that holds
// snapshots under root. A root without a read-through cache claims nothing.
func ClaimCacheEvictionOwnership(root StorageBackend) (release func(), err error) {
	cache, _, err := CacheBackendForKey(root, "snap/owner")
	if err != nil || cache == nil {
		return func() {}, err
	}
	return cache.ClaimEvictionOwnership()
}

// EnforceBudget evicts until the cache is within its budget and above its
// free-space floor. The eviction owner calls it periodically, because the
// other processes no longer evict what it keeps.
func (c *LocalCacheBackend) EnforceBudget() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enforceBudgetLocked("")
}

// deferToOwnerLocked reports whether another process owns eviction of this
// cache. Caller holds c.mu.
func (c *LocalCacheBackend) deferToOwnerLocked() bool {
	if c.evictionOwner {
		return false
	}
	return evictionOwnerHeld(c.root)
}
