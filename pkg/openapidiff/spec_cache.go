package openapidiff

// SpecCache is an in-process LRU cache for the
// `?source=auto` generated OpenAPI spec (ADR-126 / issue #975
// item #2). Apid-process-local — no Redis, no Postgres — keyed
// on (app_id, sha(doc), sha(routes), sha(rules)) so a customer
// reading the same spec twice gets a cache hit without redoing
// the merge work.
//
// Cache invalidation triggers:
//
//  1. NotifyAppOpenAPIDocChanged (new pg_notify channel from
//     item #2) — fired when the imported doc is created,
//     replaced, or deleted.
//  2. NotifyEdgeRuleChanged (existing channel from ADR-091 /
//     item #1) — fired when an edge rule is created, updated,
//     or deleted.
//
// Both channels drive InvalidateByApp(appID) — wholesale flush
// per app (the payload doesn't carry affected IDs, and the cache
// is bounded at 256 entries so a wholesale delete is cheap).
// TTL=5min guarantees freshness even if a notify is missed.
//
// Why wholesale not per-rule: the cache key includes the SHA
// of the rules list, so a write that doesn't actually change
// the rules' union produces the same key (cache hit). Only a
// real rules change invalidates the entry. Wholesale-per-app
// flush is a single map delete; per-rule fan-out is O(rules).
// The simpler algorithm wins because the cache is small.

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// specCacheTTL is the freshness window for cached entries. 5
// minutes is the dashboard's poll interval; cache hits stay
// warm across polls while stale entries fall off the LRU.
const specCacheTTL = 5 * time.Minute

// specCacheMaxEntries caps the cache at 256 entries — one per
// app in the busiest tier-1 control plane. At ~5 KiB per entry
// the worst-case memory footprint is 1.3 MiB.
const specCacheMaxEntries = 256

// SpecCacheEntry is one cache row: the pre-rendered JSON
// payload plus the source string + annotations count the apid
// handler emits verbatim on a hit. Caching the rendered bytes
// (not just the *Spec) is what makes a hit actually cheap —
// the apid handler skips parse + merge + render entirely,
// writes the bytes, and sets the headers (issue #975 item #2
// review-fix: pre-rendered-payload layer).
type SpecCacheEntry struct {
	// Body is the rendered JSON. The apid handler writes this
	// verbatim — no marshalling on the hit path.
	Body []byte
	// Source is the source string for the response header
	// (one of "auto", "degraded: routes_unavailable", etc.).
	// Pre-computed at cache-fill time so a cache hit doesn't
	// recompute the degraded-source predicate.
	Source string
	// AnnotationsCount is the len(meta.Annotations) at
	// fill-time. Mirrored in the X-OpenAPI-Doc-Annotations-Count
	// response header — pinned at fill time so the dashboard
	// sees the same value across hit and miss for the same
	// inputs.
	AnnotationsCount int
	// GeneratedAt is the wall-clock time at which the entry was
	// created. Surfaced in the response's GeneratedAt field so
	// the dashboard can show "generated N seconds ago".
	GeneratedAt time.Time
}

// SpecCache is the in-process LRU. Safe for concurrent use.
type SpecCache struct {
	mu      sync.Mutex
	cap     int
	ttl     time.Duration
	entries map[string]*list.Element // key -> list element
	lru     *list.List               // front = most recently used
	clock   func() time.Time         // injectable for tests
}

// cacheListEntry is the list element value. Holds the cache
// key so InvalidateByApp can scan the LRU and remove matching
// entries without a separate map reverse-lookup.
type cacheListEntry struct {
	key   string
	value *SpecCacheEntry
}

// NewSpecCache returns a fresh SpecCache with the default
// capacity (256) and TTL (5 min). The clock is time.Now; tests
// inject a deterministic clock via NewSpecCacheWithClock.
func NewSpecCache() *SpecCache {
	return NewSpecCacheWithClock(specCacheMaxEntries, specCacheTTL, time.Now)
}

// NewSpecCacheWithClock is the test-friendly constructor. cap
// is the maximum number of entries; ttl is the freshness window;
// clock is the time source (production: time.Now; tests: a
// step-able fake).
func NewSpecCacheWithClock(cap int, ttl time.Duration, clock func() time.Time) *SpecCache {
	if cap <= 0 {
		cap = specCacheMaxEntries
	}
	if ttl <= 0 {
		ttl = specCacheTTL
	}
	if clock == nil {
		clock = time.Now
	}
	return &SpecCache{
		cap:     cap,
		ttl:     ttl,
		entries: make(map[string]*list.Element, cap),
		lru:     list.New(),
		clock:   clock,
	}
}

// CacheKey returns the canonical cache key for an (appID,
// docSHA, routesSHA, rulesSHA) tuple. Stable across calls;
// two equal inputs produce the same key. Exposed for testing
// (the test asserts that two equivalent tuples hash to the
// same key) and for diagnostics.
func CacheKey(appID string, docSHA, routesSHA, rulesSHA [32]byte) string {
	return fmt.Sprintf("%s/%x/%x/%x", appID, docSHA, routesSHA, rulesSHA)
}

// Get returns the cached spec for the given inputs. The second
// return is true on a hit (entry present AND within TTL), false
// on a miss or expired entry.
func (c *SpecCache) Get(appID string, docSHA, routesSHA, rulesSHA [32]byte) (*SpecCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := CacheKey(appID, docSHA, routesSHA, rulesSHA)
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	// Move-to-front: refresh LRU position.
	c.lru.MoveToFront(el)
	entry := el.Value.(*cacheListEntry)
	// TTL check. An expired entry is treated as a miss and
	// evicted from the cache so subsequent Get calls return
	// miss cleanly without us keeping dead weight.
	if c.clock().Sub(entry.value.GeneratedAt) > c.ttl {
		c.removeElement(el)
		return nil, false
	}
	return entry.value, true
}

// Put inserts (or overwrites) a cache entry. body is the
// pre-rendered JSON bytes the apid handler will write verbatim
// on a hit; source + annotationsCount are the response-header
// values pre-computed at fill time. LRU-evicts the oldest
// entry when the cap is exceeded.
func (c *SpecCache) Put(appID string, docSHA, routesSHA, rulesSHA [32]byte, body []byte, source string, annotationsCount int, generatedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := CacheKey(appID, docSHA, routesSHA, rulesSHA)
	entry := &SpecCacheEntry{
		Body:             body,
		Source:           source,
		AnnotationsCount: annotationsCount,
		GeneratedAt:      generatedAt,
	}
	if el, ok := c.entries[key]; ok {
		// Overwrite: refresh value + LRU position.
		el.Value.(*cacheListEntry).value = entry
		c.lru.MoveToFront(el)
		return
	}
	// Insert: front-of-list for LRU.
	el := c.lru.PushFront(&cacheListEntry{key: key, value: entry})
	c.entries[key] = el
	// Evict from the back if we're over cap.
	for c.lru.Len() > c.cap {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.removeElement(oldest)
	}
}

// InvalidateByApp removes all cache entries for the given appID.
// Wholesale: the cache is small and the payload doesn't carry
// the affected rule/doc IDs.
func (c *SpecCache) InvalidateByApp(appID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := appID + "/"
	for el := c.lru.Front(); el != nil; {
		next := el.Next()
		entry, ok := el.Value.(*cacheListEntry)
		if !ok {
			el = next
			continue
		}
		if len(entry.key) >= len(prefix) && entry.key[:len(prefix)] == prefix {
			c.removeElement(el)
		}
		el = next
	}
}

// Len returns the current number of cached entries. Exposed for
// the dashboard's cache-hit metric.
func (c *SpecCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// removeElement is the un-locked helper for the LRU + map
// removal. Caller must hold c.mu.
func (c *SpecCache) removeElement(el *list.Element) {
	c.lru.Remove(el)
	delete(c.entries, el.Value.(*cacheListEntry).key)
}

// SumSHA256 is the small helper that hashes a byte slice into
// the [32]byte cache-key half. Used by the apid handler when
// computing the (docSHA, routesSHA, rulesSHA) input triple.
// Lives here (not pkg/api) so the cache and its keys stay
// co-located.
func SumSHA256(b []byte) [32]byte {
	return sha256.Sum256(b)
}
