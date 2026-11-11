package gateway

import "context"

type responseCacheFill struct {
	cache      *ResponseCache
	generation uint64
}

type responseCacheFillContextKey struct{}

func (c *ResponseCache) beginFill() *responseCacheFill {
	if c == nil {
		return nil
	}
	c.fillMu.RLock()
	defer c.fillMu.RUnlock()
	return &responseCacheFill{cache: c, generation: c.generation}
}

// Capture before policy resolution, not just before the origin call: a request
// can still hold a pinned old policy after the corresponding cache purge.
func withResponseCacheFill(ctx context.Context, cache *ResponseCache) context.Context {
	if cache == nil {
		return ctx
	}
	if fill, _ := ctx.Value(responseCacheFillContextKey{}).(*responseCacheFill); fill != nil && fill.cache == cache {
		return ctx
	}
	return context.WithValue(ctx, responseCacheFillContextKey{}, cache.beginFill())
}

func responseCacheFillFrom(ctx context.Context, cache *ResponseCache) *responseCacheFill {
	if fill, _ := ctx.Value(responseCacheFillContextKey{}).(*responseCacheFill); fill != nil && fill.cache == cache {
		return fill
	}
	return cache.beginFill()
}

// Account for keys, headers, tags and action slices as well as the body. The
// fixed allowance conservatively covers entry/list/map and slice bookkeeping,
// so even empty responses consume budget and bound the number of entries.
func responseCacheEntryBytes(entry *cacheEntry) int {
	const overhead = 1024
	size := overhead + len(entry.encodedKey) + len(entry.body)
	for _, field := range []string{entry.key.AppID, entry.key.DeploymentID, entry.key.RuleID, entry.key.Method, entry.key.NormalizedPath, entry.key.RawPath, entry.key.Query, entry.servedDeploymentID} {
		size += len(field)
	}
	for name, values := range entry.header {
		size += len(name) + 64
		for _, value := range values {
			size += len(value) + 16
		}
	}
	for _, tag := range entry.tags {
		size += len(tag) + 16
	}
	if entry.ruleAction != nil {
		for _, value := range entry.ruleAction.VaryOn {
			size += len(value) + 16
		}
		for _, value := range entry.ruleAction.Methods {
			size += len(value) + 16
		}
	}
	return size
}
