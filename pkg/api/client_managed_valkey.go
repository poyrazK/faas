package api

import (
	"context"
	"net/http"
	"net/url"
)

// ListRedisCaches lists the account's managed Redis caches.
func (c *Client) ListRedisCaches(ctx context.Context) (ListRedisCachesResponse, error) {
	var out ListRedisCachesResponse
	err := c.do(ctx, http.MethodGet, "/v1/redis", nil, &out)
	return out, err
}

// CreateRedisCache creates a managed Redis cache.
func (c *Client) CreateRedisCache(ctx context.Context, request CreateRedisCacheRequest) (RedisCache, error) {
	var out RedisCache
	err := c.do(ctx, http.MethodPost, "/v1/redis", request, &out)
	return out, err
}

// GetRedisCache reads one managed Redis cache.
func (c *Client) GetRedisCache(ctx context.Context, name string) (RedisCache, error) {
	var out RedisCache
	err := c.do(ctx, http.MethodGet, "/v1/redis/"+url.PathEscape(name), nil, &out)
	return out, err
}

// DeleteRedisCache deletes a managed Redis cache and its data.
func (c *Client) DeleteRedisCache(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/redis/"+url.PathEscape(name), nil, nil)
}

// UpdateRedisCache resizes a cache or changes its eviction policy, keeping
// its data.
func (c *Client) UpdateRedisCache(ctx context.Context, name string, request UpdateRedisCacheRequest) (RedisCacheChange, error) {
	var out RedisCacheChange
	err := c.do(ctx, http.MethodPatch, "/v1/redis/"+url.PathEscape(name), request, &out)
	return out, err
}

// UpgradeRedisCache moves a cache to the current Valkey release, keeping its
// data.
func (c *Client) UpgradeRedisCache(ctx context.Context, name string) (RedisCacheChange, error) {
	var out RedisCacheChange
	err := c.do(ctx, http.MethodPost, "/v1/redis/"+url.PathEscape(name)+"/upgrade", nil, &out)
	return out, err
}

// RestartRedisCache restarts a cache's VM, keeping its data.
func (c *Client) RestartRedisCache(ctx context.Context, name string) (AppRestartResponse, error) {
	var out AppRestartResponse
	err := c.do(ctx, http.MethodPost, "/v1/redis/"+url.PathEscape(name)+"/restart", nil, &out)
	return out, err
}

// BindRedisCache writes the cache's connection URL into an app's env.
func (c *Client) BindRedisCache(ctx context.Context, name string, request BindRedisCacheRequest) (RedisCacheBinding, error) {
	var out RedisCacheBinding
	err := c.do(ctx, http.MethodPost, "/v1/redis/"+url.PathEscape(name)+"/bindings", request, &out)
	return out, err
}

// UnbindRedisCache revokes an app's grant to call a cache.
func (c *Client) UnbindRedisCache(ctx context.Context, name, app string) (RedisCacheUnbinding, error) {
	var out RedisCacheUnbinding
	err := c.do(ctx, http.MethodDelete, "/v1/redis/"+url.PathEscape(name)+"/bindings/"+url.PathEscape(app), nil, &out)
	return out, err
}
