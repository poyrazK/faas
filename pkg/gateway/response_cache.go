package gateway

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ResponseCache (ADR-122 §Decision) is the in-process per-gateway
// response cache for kind=cache edge rules. Three properties
// matter:
//
//  1. Bounded by a byte ceiling. The store tracks the sum of all
//     live entry byte sizes; once total >= MaxBytes, the LRU
//     entry is evicted (and its size subtracted) until a new
//     Put fits. A single misconfigured rule cannot pin the
//     gateway's resident memory.
//  2. Lazy expiry on Get. Entries past their fresh window are
//     dropped on read; the in-memory budget is reclaimed when
//     the LRU eviction catches up, or immediately if the next
//     Put needs the headroom. No background sweeper goroutine
//     — same posture as public_auth_cache.go (allocation-light,
//     daemon-reload-friendly, no goroutine-leak surface).
//  3. Per-app invalidation. db.NotifyEdgeRuleChanged triggers
//     InvalidateByApp (cache rules reference appID). A future
//     PR plumbing CacheKey.DeploymentID from the picker path
//     will add per-deployment invalidation at that time;
//     until then, deploy invalidation flows through the same
//     NotifyAppChanged hook as a coarser (whole-app) flush
//     (cmd/gatewayd-internal/backend.go::handleInvalidation).
//
// The cache is NOT shared across gatewayd-internal instances
// (ADR-122 D7): it lives in process memory, has no Redis or
// shared-cache backing, and does not survive a daemon restart.
// Customers who need a durable shared cache are directed to the
// existing docs/storage.md recommendation (Upstash Redis). The
// platform-owned cache is a wake-elision lever, not a KV
// replacement.
//
// Cacheability is deny-by-default at the call site (see the
// isCacheable predicate in handler_apply_edge_rule_cache.go).
// The store itself trusts the caller; this matches the
// public_auth_cache posture (no schema-level enforcement).
type ResponseCache struct {
	maxBytes int
	now      func() time.Time
	mu       sync.Mutex
	// data maps CacheKey → *list.Element holding *cacheEntry.
	// list is the LRU; Front = most recently used, Back = LRU.
	data map[string]*list.Element
	list *list.List
	// bytes tracks the current sum of entry.body sizes. Decremented
	// on evict / drop. Bounded by maxBytes; Put never grows the
	// store past the ceiling.
	bytes int
}

// CacheKey is the closed set of dimensions that uniquely
// identifies one cached response. The runtime computes it from
// the matched rule + request fields in
// handler_apply_edge_rule_cache.go::computeCacheKey.
//
// AppID + DeploymentID + RuleID partition the cache so two apps
// (or two releases of one app) can never cross-serve. Method +
// Path + Query discriminate per-route (Query is the SORTED
// r.URL.RawQuery — sorting is the caller's job because the
// applier already needs to canonicalise for the rule matcher;
// without the sort, ?id=1&id=2 and ?id=2&id=1 would collide,
// silently serving the wrong body). VaryHash is the sha-256 of
// the (sorted, lower-cased) vary_on header values, so the same
// (host, path, vary) tuple is a single key.
//
// Method is included because a kind=cache rule can target
// {GET, HEAD} — HEAD requests are common from monitoring agents
// and must not share a body with GET. NormalizedPath is the
// post-glob match path (per the matched rule's match_path),
// bounded by the rule cardinality so an unbounded per-path
// attacker can't enumerate.
type CacheKey struct {
	AppID          string
	DeploymentID   string
	RuleID         string
	Method         string
	NormalizedPath string
	Query          string
	VaryHash       [32]byte
}

// String returns a stable hex form for logging + tests. The
// hot path ALSO calls String (it's the map key for Get/Put),
// so this MUST be a stable, collision-free encoding of every
// field — including long NormalizedPath / Query strings. The
// previous fixed-[404]byte buffer implementation silently
// truncated fields past 293 bytes, which caused distinct
// URLs to collide on the same map key. The fix is a dynamic
// buffer; the hex envelope is 2 bytes per source byte, so
// the allocation is bounded by the size of the input fields,
// not by a guessed ceiling.
//
// Field order is fixed so two keys with identical fields
// always encode to the same string:
//
//	VaryHash | AppID | ':' | DeploymentID | ':' | RuleID |
//	':' | Method | ':' | NormalizedPath | ':' | Query
func (k CacheKey) String() string {
	// Pre-size: 32 (varyhash) + len(fields) + 5 separators.
	n := 32 + len(k.AppID) + len(k.DeploymentID) + len(k.RuleID) +
		len(k.Method) + len(k.NormalizedPath) + len(k.Query) + 5
	b := make([]byte, 0, n)
	b = append(b, k.VaryHash[:]...)
	b = append(b, k.AppID...)
	b = append(b, ':')
	b = append(b, k.DeploymentID...)
	b = append(b, ':')
	b = append(b, k.RuleID...)
	b = append(b, ':')
	b = append(b, k.Method...)
	b = append(b, ':')
	b = append(b, k.NormalizedPath...)
	b = append(b, ':')
	b = append(b, k.Query...)
	return hex.EncodeToString(b)
}

// cacheEntry is the LRU list element. Body is the cached
// response body bytes (caller-supplied; the store does not
// parse it). Header is the captured response header map
// captured BEFORE the body was written, so a hit can replay
// the original response shape verbatim. StatusCode is the
// 2xx/3xx status that was served; the applier gates on this
// at cacheability time (only cacheable statuses are stored).
//
// FreshUntil is the absolute fresh-window expiry; stale-on-error
// serves look at StaleUntil instead. Both captured at Put() time
// using the cache's now() clock so test code can advance time.
type cacheEntry struct {
	key        CacheKey
	statusCode int
	header     map[string][]string
	body       []byte
	freshUntil time.Time
	staleUntil time.Time
	// ruleAction carries the per-rule knobs that the serve path
	// needs to know without re-resolving the rule: stale-on-error
	// served the entry as Warning: 110, and the saved-cost metric
	// uses the per-rule plan RAM to compute "wakes avoided".
	ruleAction *state.EdgeRuleCacheAction
}

// Default response-cache sizing constants. These are the
// platform defaults; per-rule byte cap (1 MiB) is enforced at
// the applier, not here, so a single Put cannot exceed
// PerEntryMaxBytes regardless of how much room the store has.
const (
	// DefaultResponseCacheMaxBytes is the in-process ceiling.
	// 64 MiB matches pkg/gateway/routes.go's route-cache
	// posture: enough headroom for ~64 typical 1-MiB catalogue
	// pages before the LRU evicts the oldest.
	DefaultResponseCacheMaxBytes = 64 * 1024 * 1024
	// ResponseCachePerEntryMaxBytes is the per-entry cap. A
	// single Put that exceeds this is rejected (NOT truncated)
	// so the applier can count "store_skipped" against the
	// gateway_response_cache_total{outcome} metric. Larger
	// payloads must be served directly; the rule author should
	// either tighten the path glob or accept that the route
	// will not be cache-eligible.
	ResponseCachePerEntryMaxBytes = 1 * 1024 * 1024
)

// NewResponseCache constructs a cache with the default 64 MiB
// byte ceiling and the wall-clock time source. Production wires
// this from cmd/gatewayd-internal/main.go; tests prefer
// NewResponseCacheWithClock + NewResponseCacheWithMaxBytes so
// they can advance time + shrink the ceiling.
func NewResponseCache() *ResponseCache {
	return NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
}

// NewResponseCacheWithClock lets tests inject a custom byte
// ceiling + clock. maxBytes=0 collapses to a no-op cache
// (every Get is a miss; Put rejects anything but a no-op); the
// hot path handles nil-receiver pass-through too. clock=nil
// falls back to time.Now. The clock-shaped now func means
// tests can advance the wall clock by reassigning the field
// directly (mutating the returned cache is safe; the mu lock
// guards every operation).
func NewResponseCacheWithClock(maxBytes int, now func() time.Time) *ResponseCache {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if now == nil {
		now = time.Now
	}
	return &ResponseCache{
		maxBytes: maxBytes,
		now:      now,
		data:     make(map[string]*list.Element),
		list:     list.New(),
	}
}

// Get returns the entry for k, or nil on miss / expiry.
//
//   - Fresh hit (now < entry.freshUntil): returns ("fresh",
//     entry).
//   - Stale-on-error-eligible (now < entry.staleUntil): returns
//     ("stale_if_error_eligible", entry). The applier only
//     serves this state on a genuine origin failure; on a
//     cache miss, the entry is NOT used (the per-applier
//     rule is: stale serves only on wake failure or upstream
//     5xx/timeout, never on a normal miss).
//   - Expired (now >= entry.staleUntil): drops the entry on the
//     floor (returns nil) and the next Put needs the headroom.
//
// Expired-and-stale entries ARE dropped on Get to keep the
// store from leaking entries that no rule can ever serve
// again. The post-fresh window is small enough (≤ 5 min per
// EdgeRuleCacheAction.StaleIfErrorSeconds) that this is
// bounded.
func (c *ResponseCache) Get(k CacheKey) (state string, entry *cacheEntry) {
	if c == nil {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.data[k.String()]
	if !ok {
		return "", nil
	}
	entry = el.Value.(*cacheEntry)
	now := c.now()
	if now.Before(entry.freshUntil) {
		// Fresh hit. Promote to MRU.
		c.list.MoveToFront(el)
		return "fresh", entry
	}
	if now.Before(entry.staleUntil) {
		// Past fresh, inside stale-on-error window. The
		// applier checks the outcome under the wake-failure
		// gate; on a normal miss the entry is NOT served
		// (otherwise we'd be inventing stale hits on every
		// cache miss past max_age).
		c.list.MoveToFront(el)
		return "stale_if_error_eligible", entry
	}
	// Expired. Drop and reclaim headroom immediately so the
	// next Put doesn't have to wait for the LRU eviction.
	c.list.Remove(el)
	delete(c.data, k.String())
	c.bytes -= len(entry.body)
	if c.bytes < 0 {
		c.bytes = 0
	}
	return "", nil
}

// Put inserts an entry under k. If the entry's body size
// exceeds ResponseCachePerEntryMaxBytes, Put is a no-op and
// returns false so the caller can record the
// gateway_response_cache_total{outcome="store_skipped"}
// counter.
//
// Otherwise Put evicts LRU entries until the new entry fits
// (or until the store is empty, in which case the entry is
// rejected if its body is still larger than maxBytes — that
// would mean maxBytes < PerEntryMaxBytes, which is a
// misconfiguration the test surface catches).
//
// freshUntil is the fresh window expiry; staleUntil is the
// stale-on-error upper bound. Pass staleUntil ≤ freshUntil to
// disable stale-on-error (the applier passes the per-rule
// StaleIfErrorSeconds, which is 0 → no stale window).
func (c *ResponseCache) Put(k CacheKey, statusCode int, header map[string][]string, body []byte, freshUntil, staleUntil time.Time, ruleAction *state.EdgeRuleCacheAction) bool {
	if c == nil {
		return false
	}
	if len(body) > ResponseCachePerEntryMaxBytes {
		// Per-entry cap veto. The applier counts this as
		// outcome="store_skipped" so a misconfigured rule is
		// visible in dashboards.
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// If we already have this key (race against a concurrent
	// Put on the same key, e.g. two cold-boot threads), drop
	// the old entry's bytes from the running total before
	// inserting.
	if el, ok := c.data[k.String()]; ok {
		old := el.Value.(*cacheEntry)
		c.bytes -= len(old.body)
		c.list.Remove(el)
		delete(c.data, k.String())
	}
	// Evict LRU until the new entry fits.
	for c.bytes+len(body) > c.maxBytes && c.list.Len() > 0 {
		back := c.list.Back()
		if back == nil {
			break
		}
		victim := back.Value.(*cacheEntry)
		c.bytes -= len(victim.body)
		c.list.Remove(back)
		delete(c.data, victim.key.String())
	}
	if len(body) > c.maxBytes {
		// Even after evicting everything, the entry doesn't
		// fit. Reject and leave the store empty.
		return false
	}
	entry := &cacheEntry{
		key:        k,
		statusCode: statusCode,
		header:     copyHeader(header),
		body:       append([]byte(nil), body...),
		freshUntil: freshUntil,
		staleUntil: staleUntil,
		ruleAction: ruleAction,
	}
	el := c.list.PushFront(entry)
	c.data[k.String()] = el
	c.bytes += len(body)
	return true
}

// InvalidateByApp drops every cached entry whose key has the
// given AppID. Used by the db.NotifyEdgeRuleChanged handler
// (cmd/gatewayd-internal) so a new cache rule reaches the
// gateway within ~1s instead of waiting for the TTL.
func (c *ResponseCache) InvalidateByApp(appID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, el := range c.data {
		entry := el.Value.(*cacheEntry)
		if entry.key.AppID == appID {
			c.bytes -= len(entry.body)
			c.list.Remove(el)
			delete(c.data, k)
		}
	}
	if c.bytes < 0 {
		c.bytes = 0
	}
}

// InvalidateByAppPath drops cached entries for appID whose normalized path
// matches pathGlob. An empty glob or "*" is an app-wide purge. The glob uses
// the same path.Match semantics as edge-rule path matching.
func (c *ResponseCache) InvalidateByAppPath(appID, pathGlob string) error {
	if c == nil {
		return nil
	}
	if appID == "" {
		return fmt.Errorf("cache purge app id is required")
	}
	if pathGlob == "" || pathGlob == "*" {
		c.InvalidateByApp(appID)
		return nil
	}
	if _, err := pathGlobMatch(pathGlob, "/"); err != nil {
		return fmt.Errorf("invalid cache path glob %q: %w", pathGlob, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, el := range c.data {
		entry := el.Value.(*cacheEntry)
		if entry.key.AppID != appID {
			continue
		}
		matches, err := pathGlobMatch(pathGlob, entry.key.NormalizedPath)
		if err != nil {
			return fmt.Errorf("invalid cache path glob %q: %w", pathGlob, err)
		}
		if matches {
			c.bytes -= len(entry.body)
			c.list.Remove(el)
			delete(c.data, k)
		}
	}
	if c.bytes < 0 {
		c.bytes = 0
	}
	return nil
}

// InvalidateAll drops every cached entry. Used by the
// db.NotifyEdgeRuleChanged path when the rule payload
// references an appID we cannot extract cleanly (defensive —
// in practice the applier always parses a fresh payload), and
// by tests that want a clean slate without waiting for the
// TTL.
func (c *ResponseCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]*list.Element)
	c.list = list.New()
	c.bytes = 0
}

// Len returns the current entry count. Used by tests + the
// gateway_response_cache_entries gauge (commit 15).
func (c *ResponseCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.list.Len()
}

// Bytes returns the current store byte size. Used by tests +
// the gateway_response_cache_bytes gauge (commit 15).
func (c *ResponseCache) Bytes() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// HashCacheKey is the helper for computing the vary-on
// dimension of CacheKey.VaryHash. It returns sha-256 over the
// joined sorted header values. Callers pass the already-fetched
// values for the headers named in the matched rule's
// VaryOn slice; the applier is responsible for sorting and
// lower-casing per the cache key spec
// (handler_apply_edge_rule_cache.go::computeCacheKey).
func HashCacheKey(values []string) [32]byte {
	return sha256.Sum256([]byte(joinSortedValues(values)))
}

// joinSortedValues produces the canonical "vary on" hash input.
// The applier is responsible for sorting before calling; this
// function only joins so the hash is stable across order
// permutations of the same input slice.
func joinSortedValues(values []string) string {
	// Pre-sort is the caller's job (handler_apply_edge_rule_cache.go
	// sorts in computeCacheKey). This is a defensive copy +
	// join; in the common case the input is already sorted and
	// the copy is a no-op.
	out := make([]byte, 0, len(values)*16)
	for i, v := range values {
		if i > 0 {
			out = append(out, 0x1f) // unit separator
		}
		out = append(out, v...)
	}
	return string(out)
}

// copyHeader defensively clones the header map so a subsequent
// mutation on the live response writer's header map does not
// alias the cache entry's stored value. nil → nil (a 200 with
// no headers round-trips with no allocation).
func copyHeader(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, vs := range in {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}
