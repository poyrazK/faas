package storage

// adr: 20261026181418
//
// Generation-routed runtime bases. A node boots a runtime base under its
// logical key (base/base-amd64.ext4) and keeps it in this cache under that
// name, with the generation marker vmmd checks. Shared storage used to hold
// one copy of that key for the whole fleet, so whichever release staged last
// decided the generation every other node fetched. On production-us
// (2026-10-26) an rc.294 imaged on fsn-3 restaged base-minimal at 17:03,
// flipped the shared scan sidecar back to rc.294's generation, and fsn-2's
// rc.296 vmmd refused every cold boot from then on.
//
// A routed key keeps its logical name in this cache, but every parent read
// and write of it uses the generation's own key (storagekey.BaseGeneration).
// A release configured for another generation routes to another key, so
// neither can overwrite what the other boots. The routes are set once at
// startup from the generations the daemon's release configures.

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// RouteGeneration makes every parent read and write of key use parentKey,
// while this cache keeps the entry, and its generation marker, under key.
// It reports false and routes nothing when the parent keeps key, or
// parentKey, on a node-local backend: that file is the copy the node boots,
// and no other node shares it.
func (c *LocalCacheBackend) RouteGeneration(key, parentKey string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	if err := validateKey(parentKey); err != nil {
		return false, err
	}
	if key == parentKey {
		return false, fmt.Errorf("storage: cache: route %q to itself", key)
	}
	if routesLocally(c.parent, key) || routesLocally(c.parent, parentKey) {
		return false, nil
	}
	c.routesMu.Lock()
	defer c.routesMu.Unlock()
	if c.routes == nil {
		c.routes = make(map[string]string)
	}
	c.routes[key] = parentKey
	return true, nil
}

// GenerationRoute reports the parent key a routed key reads and writes.
func (c *LocalCacheBackend) GenerationRoute(key string) (string, bool) {
	c.routesMu.RLock()
	defer c.routesMu.RUnlock()
	parentKey, ok := c.routes[key]
	return parentKey, ok
}

// parentKey is the name key has in the parent backend.
func (c *LocalCacheBackend) parentKey(key string) string {
	if parentKey, ok := c.GenerationRoute(key); ok {
		return parentKey
	}
	return key
}

// GetParent reads parentKey from the canonical parent exactly as named,
// without generation routing, and leaves this cache untouched. It reads the
// legacy shared copy of a routed key.
func (c *LocalCacheBackend) GetParent(ctx context.Context, parentKey string) (io.ReadCloser, error) {
	if err := validateKey(parentKey); err != nil {
		return nil, err
	}
	rc, err := c.parent.Get(ctx, parentKey)
	if err != nil {
		return nil, fmt.Errorf("storage: cache: get parent %q: %w", parentKey, err)
	}
	return rc, nil
}

// RefreshFrom replaces key's cache entry with the canonical parent object
// parentKey, read exactly as named, and returns it. vmmd uses it to adopt one
// generation of a runtime base under the logical name it boots.
func (c *LocalCacheBackend) RefreshFrom(ctx context.Context, key, parentKey string) (io.ReadCloser, error) {
	if err := validateKey(parentKey); err != nil {
		return nil, err
	}
	return c.fetch(ctx, key, parentKey, true)
}

// PublishCached uploads this node's cached copy of key to key's parent name
// when the parent does not hold it, and reports whether it uploaded. imaged
// uses it when a reused base is verified from this cache but its generation
// was never published under its own key.
func (c *LocalCacheBackend) PublishCached(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	parentKey := c.parentKey(key)
	held, err := c.parentHolds(ctx, parentKey)
	if err != nil || held {
		return false, err
	}
	cached, ok := c.openCache(key)
	if !ok {
		return false, fmt.Errorf("storage: cache: publish %q: %w", key, ErrNotFound)
	}
	defer func() { _ = cached.Close() }()
	if err := c.parent.Put(ctx, parentKey, cached); err != nil {
		return false, fmt.Errorf("storage: cache: publish %q as %q: %w", key, parentKey, err)
	}
	return true, nil
}

func (c *LocalCacheBackend) parentHolds(ctx context.Context, parentKey string) (bool, error) {
	if held, supported, err := Exists(ctx, c.parent, parentKey); supported {
		return held, err
	}
	rc, err := c.parent.Get(ctx, parentKey)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, rc.Close()
}

// RoutesLocally reports whether the parent keeps key on a node-local
// filesystem, where RouteGeneration routes nothing.
func (c *LocalCacheBackend) RoutesLocally(key string) bool {
	return routesLocally(c.parent, key)
}

// routesLocally reports whether be keeps key on a node-local filesystem.
func routesLocally(be StorageBackend, key string) bool {
	switch b := be.(type) {
	case *LocalStorageBackend:
		return true
	case *PrefixRouter:
		child, rest, _, err := b.dispatch(key)
		return err == nil && routesLocally(child, rest)
	}
	return false
}

// RouteGeneration routes key to parentKey in the read-through cache that owns
// key (LocalCacheBackend.RouteGeneration). It reports false when no cache owns
// key, or when its parent keeps key locally. Both keys must be owned by the
// same cache.
func RouteGeneration(root StorageBackend, key, parentKey string) (bool, error) {
	cache, rel, err := CacheBackendForKey(root, key)
	if err != nil || cache == nil {
		return false, err
	}
	parentCache, parentRel, err := CacheBackendForKey(root, parentKey)
	if err != nil {
		return false, err
	}
	if parentCache != cache {
		return false, fmt.Errorf("storage: route %q to %q: the keys have different caches", key, parentKey)
	}
	return cache.RouteGeneration(rel, parentRel)
}

// GetParent reads key from the canonical store exactly as named, without the
// generation routing of the cache that owns it (LocalCacheBackend.GetParent).
func GetParent(ctx context.Context, root StorageBackend, key string) (io.ReadCloser, error) {
	cache, rel, err := CacheBackendForKey(root, key)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		return cache.GetParent(ctx, rel)
	}
	if root == nil {
		return nil, fmt.Errorf("storage: get parent %q: no storage backend", key)
	}
	return root.Get(ctx, key)
}
