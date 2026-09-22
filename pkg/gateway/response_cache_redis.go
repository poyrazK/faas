package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	redisResponseCachePrefix       = "gregale:response-cache:v1"
	redisResponseCacheVersion      = 1
	redisResponseCacheIOTimeout    = 100 * time.Millisecond
	redisResponseCacheAdminTimeout = 2 * time.Second
)

// RedisResponseCache is the optional distributed L2 for declarative response
// caching. Redis is never authoritative: callers fail open to the local L1 or
// origin whenever an operation returns an error.
type RedisResponseCache struct {
	client redis.UniversalClient
}

type redisResponseCacheRecord struct {
	Version         int                        `json:"version"`
	Key             CacheKey                   `json:"key"`
	StatusCode      int                        `json:"status_code"`
	Header          map[string][]string        `json:"header"`
	Body            []byte                     `json:"body"`
	FreshUntil      time.Time                  `json:"fresh_until"`
	RevalidateUntil time.Time                  `json:"revalidate_until"`
	ErrorUntil      time.Time                  `json:"error_until"`
	RuleAction      *state.EdgeRuleCacheAction `json:"rule_action,omitempty"`
}

// NewRedisResponseCache connects to a redis:// or rediss:// endpoint and
// verifies it before the gateway begins serving. The caller may treat an error
// as a signal to retain local-only caching.
func NewRedisResponseCache(parentCtx context.Context, rawURL string) (*RedisResponseCache, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		// rawURL may contain credentials; do not allow parser details to put
		// any portion of the secret URL into gateway logs.
		return nil, errors.New("invalid response-cache Redis URL")
	}
	opts.DialTimeout = redisResponseCacheAdminTimeout
	opts.ReadTimeout = redisResponseCacheIOTimeout
	opts.WriteTimeout = redisResponseCacheIOTimeout
	opts.PoolSize = 8
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(parentCtx, redisResponseCacheAdminTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping response-cache Redis: %w", err)
	}
	return &RedisResponseCache{client: client}, nil
}

func (c *RedisResponseCache) Get(key CacheKey) (*cacheEntry, error) {
	if c == nil || c.client == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisResponseCacheIOTimeout)
	defer cancel()
	raw, err := c.client.Get(ctx, redisResponseCacheKey(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record redisResponseCacheRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode response-cache record: %w", err)
	}
	if record.Version != redisResponseCacheVersion || record.Key != key {
		return nil, errors.New("response-cache record identity mismatch")
	}
	return record.cacheEntry(), nil
}

func (c *RedisResponseCache) Put(entry *cacheEntry) error {
	if c == nil || c.client == nil || entry == nil {
		return nil
	}
	ttl := time.Until(entry.staleUntil)
	if ttl <= 0 {
		return nil
	}
	record := redisResponseCacheRecord{
		Version:         redisResponseCacheVersion,
		Key:             entry.key,
		StatusCode:      entry.statusCode,
		Header:          copyHeader(entry.header),
		Body:            append([]byte(nil), entry.body...),
		FreshUntil:      entry.freshUntil,
		RevalidateUntil: entry.revalidateUntil,
		ErrorUntil:      entry.errorUntil,
		RuleAction:      copyCacheRuleAction(entry.ruleAction),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode response-cache record: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisResponseCacheIOTimeout)
	defer cancel()
	return c.client.Set(ctx, redisResponseCacheKey(entry.key), raw, ttl).Err()
}

func (c *RedisResponseCache) InvalidateByApp(appID string) error {
	if appID == "" {
		return fmt.Errorf("cache purge app id is required")
	}
	return c.unlinkPattern(redisResponseCacheAppPattern(appID), nil)
}

func (c *RedisResponseCache) InvalidateByAppPath(appID, pathGlob string) error {
	if appID == "" {
		return fmt.Errorf("cache purge app id is required")
	}
	if pathGlob == "" || pathGlob == "*" {
		return c.InvalidateByApp(appID)
	}
	if _, err := pathGlobMatch(pathGlob, "/"); err != nil {
		return fmt.Errorf("invalid cache path glob %q: %w", pathGlob, err)
	}
	return c.unlinkPattern(redisResponseCacheAppPattern(appID), func(raw []byte) (bool, error) {
		record, ok := decodeRedisResponseCacheRecord(raw)
		if !ok {
			// A corrupt record cannot safely match a path, but it is already
			// scoped to this app by the key prefix. Purging it is safer than
			// leaving an undeletable entry behind.
			return true, nil
		}
		return pathGlobMatch(pathGlob, record.Key.NormalizedPath)
	})
}

func decodeRedisResponseCacheRecord(raw []byte) (redisResponseCacheRecord, bool) {
	var record redisResponseCacheRecord
	if json.Unmarshal(raw, &record) != nil {
		return redisResponseCacheRecord{}, false
	}
	return record, true
}

func (c *RedisResponseCache) InvalidateAll() error {
	return c.unlinkPattern(redisResponseCachePrefix+":entry:*", nil)
}

func (c *RedisResponseCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// unlinkPattern deletes entries in small SCAN batches. When keep is non-nil,
// only decoded records accepted by the predicate are removed.
func (c *RedisResponseCache) unlinkPattern(pattern string, keep func([]byte) (bool, error)) error {
	if c == nil || c.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisResponseCacheAdminTimeout)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		toDelete := keys
		if keep != nil {
			toDelete = make([]string, 0, len(keys))
			for _, key := range keys {
				raw, err := c.client.Get(ctx, key).Bytes()
				if errors.Is(err, redis.Nil) {
					continue
				}
				if err != nil {
					return err
				}
				matched, err := keep(raw)
				if err != nil {
					return err
				}
				if matched {
					toDelete = append(toDelete, key)
				}
			}
		}
		if len(toDelete) > 0 {
			if err := c.client.Unlink(ctx, toDelete...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func redisResponseCacheKey(key CacheKey) string {
	digest := sha256.Sum256([]byte(key.String()))
	return redisResponseCachePrefix + ":entry:" + redisResponseCacheAppToken(key.AppID) + ":" + hex.EncodeToString(digest[:])
}

func redisResponseCacheAppPattern(appID string) string {
	return redisResponseCachePrefix + ":entry:" + redisResponseCacheAppToken(appID) + ":*"
}

func redisResponseCacheAppToken(appID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(appID))
}

func (r redisResponseCacheRecord) cacheEntry() *cacheEntry {
	staleUntil := r.ErrorUntil
	if r.RevalidateUntil.After(staleUntil) {
		staleUntil = r.RevalidateUntil
	}
	return &cacheEntry{
		key:             r.Key,
		statusCode:      r.StatusCode,
		header:          copyHeader(r.Header),
		body:            append([]byte(nil), r.Body...),
		freshUntil:      r.FreshUntil,
		revalidateUntil: r.RevalidateUntil,
		errorUntil:      r.ErrorUntil,
		staleUntil:      staleUntil,
		ruleAction:      copyCacheRuleAction(r.RuleAction),
	}
}

func copyCacheRuleAction(in *state.EdgeRuleCacheAction) *state.EdgeRuleCacheAction {
	if in == nil {
		return nil
	}
	out := *in
	out.VaryOn = append([]string(nil), in.VaryOn...)
	out.Methods = append([]string(nil), in.Methods...)
	return &out
}
