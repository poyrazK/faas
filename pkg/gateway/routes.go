package gateway

import (
	"container/list"
	"sync"
)

// RouteCache is the in-memory hostname→app_id LRU (spec §4.1: 10k entries, backed
// by Postgres LISTEN app_routes_changed; a miss is one indexed PG lookup). It is
// safe for concurrent use on the hot request path.
type RouteCache struct {
	mu   sync.Mutex
	cap  int
	ll   *list.List               // front = most recently used
	byID map[string]*list.Element // host -> element
}

type routeEntry struct {
	host  string
	appID string
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[host]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*routeEntry).appID, true
	}
	return "", false
}

// Put inserts or updates a route, evicting the least-recently-used entry if the
// cache is over capacity.
func (c *RouteCache) Put(host, appID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[host]; ok {
		el.Value.(*routeEntry).appID = appID
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&routeEntry{host: host, appID: appID})
	c.byID[host] = el
	if c.ll.Len() > c.cap {
		c.evictLRU()
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

// Reset drops every cached route. gatewayd calls this on an app/domain change
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
