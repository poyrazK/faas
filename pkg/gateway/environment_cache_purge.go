package gateway

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/wire"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// Named-environment caching stays bypassed until the durable replay has caught
// its startup baseline. Tokens partition L1/L2 and late in-flight cache writes.
func (c *ResponseCache) EnvironmentPurgeToken(app, environment, path string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.environmentPurgesReady {
		return "", false
	}
	entries := c.environmentPurges[purgeTarget(app, environment)]
	return fmt.Sprintf("%d/%d", entries[""], entries[path]), true
}
func (c *ResponseCache) MarkEnvironmentPurgesReady() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.environmentPurgesReady = true
}
func (c *ResponseCache) ApplyEnvironmentPurge(app, environment, path string, generation int64) error {
	if c == nil || app == "" || environment == "" || generation < 1 || generation > api.RouteRequirementsMaxRevision || api.ValidateEnvironmentCachePurgePath(path) != nil {
		return fmt.Errorf("invalid environment purge")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.environmentPurges == nil {
		c.environmentPurges = map[string]map[string]int64{}
	}
	target := purgeTarget(app, environment)
	if c.environmentPurges[target] == nil {
		c.environmentPurges[target] = map[string]int64{}
	}
	if c.environmentPurges[target][path] >= generation {
		return nil
	}
	c.environmentPurges[target][path] = generation
	for key, el := range c.data {
		entry := el.Value.(*cacheEntry)
		if purgeTarget(entry.key.AppID, entry.key.EnvironmentID) == target && (path == "" || entry.key.NormalizedPath == path) {
			c.bytes -= len(entry.body)
			c.list.Remove(el)
			delete(c.data, key)
		}
	}
	return nil
}

func purgeTarget(app, environment string) string {
	if id, e := uuid.Parse(app); e == nil {
		app = id.String()
	}
	if id, e := uuid.Parse(environment); e == nil {
		environment = id.String()
	}
	return app + "/" + environment
}

// Capture exactly once at lookup, before origin work. Writers and stale/refresh
// paths reuse this immutable key even if purge advances while origin is running.
type responseCacheRequestKey struct{}

func responseCacheKeyFromContext(ctx context.Context) (CacheKey, bool) {
	key, ok := ctx.Value(responseCacheRequestKey{}).(CacheKey)
	return key, ok
}
func (h *Handler) captureResponseCacheKey(r *http.Request, app App, key CacheKey) (CacheKey, bool) {
	if environment, hostApp, ok := EnvironmentIDsFromHost(wire.DeployWildcardSuffix, hostname(r.Host)); ok {
		id, err := uuid.Parse(app.ID)
		if err != nil || id.String() != hostApp {
			return key, false
		}
		token, ready := h.responseCache.EnvironmentPurgeToken(app.ID, environment, r.URL.Path)
		if !ready {
			return key, false
		}
		key.EnvironmentID = environment
		key.PurgeToken = token
	}
	*r = *r.WithContext(context.WithValue(r.Context(), responseCacheRequestKey{}, key))
	return key, true
}
