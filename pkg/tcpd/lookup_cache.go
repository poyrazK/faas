package tcpd

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// lookupCacheMaxEntries bounds each cache. Keys are public ports, app IDs,
// listener names and domains, so normal use stays far below it; past it the
// cache drops expired entries, then starts over.
const lookupCacheMaxEntries = 4096

// ttlCache keeps successful lookups for ttl. Errors are never cached.
type ttlCache[K comparable, V any] struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[K]ttlEntry[V]
}

type ttlEntry[V any] struct {
	value   V
	expires time.Time
}

func newTTLCache[K comparable, V any](ttl time.Duration) *ttlCache[K, V] {
	return &ttlCache[K, V]{ttl: ttl, now: time.Now, entries: make(map[K]ttlEntry[V])}
}

func (c *ttlCache[K, V]) get(key K, load func() (V, error)) (V, error) {
	if c.ttl <= 0 {
		return load()
	}
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.value, nil
	}
	value, err := load()
	if err != nil {
		return value, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= lookupCacheMaxEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= lookupCacheMaxEntries {
			c.entries = make(map[K]ttlEntry[V])
		}
	}
	c.entries[key] = ttlEntry[V]{value: value, expires: now.Add(c.ttl)}
	return value, nil
}

// CachedRouteResolver reuses public-port route lookups for a short TTL. Every
// accepted connection resolves its route; without the cache a connection
// flood is one Postgres query per connection before any other admission. A
// stale route is safe: StoreTargetResolver re-reads the listener intent and
// rejects a route that no longer matches it.
type CachedRouteResolver struct {
	next  RouteResolver
	cache *ttlCache[int, cachedRoute]
}

type cachedRoute struct {
	route Route
	ok    bool
}

// NewCachedRouteResolver caches next's results, including "no route", for ttl.
func NewCachedRouteResolver(next RouteResolver, ttl time.Duration) *CachedRouteResolver {
	return &CachedRouteResolver{next: next, cache: newTTLCache[int, cachedRoute](ttl)}
}

// Resolve implements RouteResolver.
func (r *CachedRouteResolver) Resolve(ctx context.Context, publicPort int) (Route, bool, error) {
	result, err := r.cache.get(publicPort, func() (cachedRoute, error) {
		route, ok, err := r.next.Resolve(ctx, publicPort)
		return cachedRoute{route: route, ok: ok}, err
	})
	return result.route, result.ok, err
}

// CachedInstanceSource reuses the domain and live-deployment reads
// StoreTargetResolver makes for every connection. The app and listener-intent
// reads stay fresh: they are what rejects a stale cached route after a
// listener is disabled or its port reassigned. Instance selection stays
// fresh too.
type CachedInstanceSource struct {
	next        InstanceSource
	domains     *ttlCache[string, state.CustomDomain]
	deployments *ttlCache[string, []state.Deployment]
}

// NewCachedInstanceSource caches next's successful reads for ttl.
func NewCachedInstanceSource(next InstanceSource, ttl time.Duration) *CachedInstanceSource {
	return &CachedInstanceSource{
		next:        next,
		domains:     newTTLCache[string, state.CustomDomain](ttl),
		deployments: newTTLCache[string, []state.Deployment](ttl),
	}
}

func (c *CachedInstanceSource) AppByID(ctx context.Context, appID string) (state.App, error) {
	return c.next.AppByID(ctx, appID)
}

func (c *CachedInstanceSource) TCPListenerByAppAndName(ctx context.Context, appID, listenerName string) (state.TCPListener, error) {
	return c.next.TCPListenerByAppAndName(ctx, appID, listenerName)
}

func (c *CachedInstanceSource) DomainByName(ctx context.Context, domain string) (state.CustomDomain, error) {
	return c.domains.get(domain, func() (state.CustomDomain, error) { return c.next.DomainByName(ctx, domain) })
}

// LiveDeployments returns a shared slice; callers must not modify it.
func (c *CachedInstanceSource) LiveDeployments(ctx context.Context, appID string) ([]state.Deployment, error) {
	return c.deployments.get(appID, func() ([]state.Deployment, error) { return c.next.LiveDeployments(ctx, appID) })
}

func (c *CachedInstanceSource) ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error) {
	return c.next.ListInstancesForApp(ctx, appID)
}
