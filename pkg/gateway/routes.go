package gateway

import (
	"container/list"
	"sync"
)

// RouteCache is the in-memory hostname→app route LRU (spec §4.1: 10k entries,
// backed by Postgres LISTEN app_routes_changed; a miss is one indexed PG
// lookup). Deployment-preview hosts also retain their host-specific revision
// pin and scope here, separate from the shared app cache.
type RouteCache struct {
	// Reads use RLock through Peek. The request path does not need to
	// promote a route on every hit: app routes are invalidated by the
	// notifier and the cache is a bounded lookup accelerator, not an
	// eviction-policy API. Get keeps the exact LRU-promoting contract for
	// callers that need it (and for backwards-compatible tests/tools).
	mu   sync.RWMutex
	cap  int
	ll   *list.List               // front = most recently used
	byID map[string]*list.Element // host -> element
}

type routeEntry struct {
	host               string
	appID              string
	pinnedDeploymentID string
	pinnedScope        string
}

// NewRouteCache returns a cache holding up to cap entries (spec §4.1: 10,000).
func NewRouteCache(capacity int) *RouteCache {
	if capacity < 1 {
		capacity = 1
	}
	return &RouteCache{cap: capacity, ll: list.New(), byID: map[string]*list.Element{}}
}

// Get returns the app_id for host and whether it was cached, promoting the entry.
func (c *RouteCache) Get(host string) (string, bool) {
	appID, _, _, ok := c.GetTarget(host)
	return appID, ok
}

// GetTarget returns the app and optional pinned deployment for host, promoting
// the entry. A deployment-preview hostname is host-specific even though its app
// shares the normal app cache entry.
func (c *RouteCache) GetTarget(host string) (string, string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[host]; ok {
		c.ll.MoveToFront(el)
		entry := el.Value.(*routeEntry)
		return entry.appID, entry.pinnedDeploymentID, entry.pinnedScope, true
	}
	return "", "", "", false
}

// Peek returns the app_id for host without updating the LRU order. It is the
// read-mostly request-path operation: concurrent route hits can proceed under
// a shared lock instead of serializing behind the LRU promotion in Get.
//
// The cache remains bounded by Put, and route invalidation remains authoritative
// through Invalidate/Reset. A hot route may therefore be evicted sooner than it
// would be with strict per-hit promotion, which is acceptable because the next
// request simply rehydrates it from the authoritative Router.
func (c *RouteCache) Peek(host string) (string, bool) {
	appID, _, _, ok := c.PeekTarget(host)
	return appID, ok
}

// PeekTarget returns the app and optional pinned deployment without promoting
// the entry on the read-mostly request path.
func (c *RouteCache) PeekTarget(host string) (string, string, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if el, ok := c.byID[host]; ok {
		entry := el.Value.(*routeEntry)
		return entry.appID, entry.pinnedDeploymentID, entry.pinnedScope, true
	}
	return "", "", "", false
}

// Put inserts or updates a route, evicting the least-recently-used entry if the
// cache is over capacity.
func (c *RouteCache) Put(host, appID string) {
	c.PutTarget(host, appID, "", "")
}

// PutTarget caches the host-specific deployment pin and scope alongside its app route.
func (c *RouteCache) PutTarget(host, appID, pinnedDeploymentID, pinnedScope string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[host]; ok {
		entry := el.Value.(*routeEntry)
		entry.appID = appID
		entry.pinnedDeploymentID = pinnedDeploymentID
		entry.pinnedScope = pinnedScope
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&routeEntry{
		host: host, appID: appID,
		pinnedDeploymentID: pinnedDeploymentID, pinnedScope: pinnedScope,
	})
	c.byID[host] = el
	if c.ll.Len() > c.cap {
		c.evictLRU()
	}
}

// InvalidateApp removes every hostname route targeting appID. Deployment
// status changes can close an immutable preview URL, so its host-level pin must
// be re-resolved even though the app itself is unchanged.
func (c *RouteCache) InvalidateApp(appID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		if el.Value.(*routeEntry).appID == appID {
			c.removeElement(el)
		}
		el = next
	}
}

// Invalidate drops host from the cache (on a route change / app delete). The next
// request re-reads it from Postgres.
func (c *RouteCache) Invalidate(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[host]; ok {
		c.removeElement(el)
	}
}

// Reset drops every cached route. gatewayd-internal calls this on an app/domain change
// notification (spec §4.1): at one-box scale (single-digit apps, spec §4.3) a
// full re-resolve on the next request is cheaper than tracking which host a
// given app_id maps to, and it can never leave a stale route behind.
func (c *RouteCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.byID = map[string]*list.Element{}
}

// Len returns the number of cached routes.
func (c *RouteCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ll.Len()
}

func (c *RouteCache) evictLRU() {
	if el := c.ll.Back(); el != nil {
		c.removeElement(el)
	}
}

func (c *RouteCache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	delete(c.byID, el.Value.(*routeEntry).host)
}
