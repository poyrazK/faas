// adr: 20261121220000 — a runtime-release base fill outlives the wake that started it.
package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// DefaultDetachedFillTimeout bounds one background fill of an immutable
// runtime-release base. A 449 MB base takes 23–37 s from OVH S3 at one
// stream's 12–19 MB/s; ten minutes leaves room for a much slower store
// while still freeing a hung connection.
const DefaultDetachedFillTimeout = 10 * time.Minute

// runtimeReleaseBasePrefix and runtimeReleaseBaseSuffix match the immutable
// runtime-release base keys state.RuntimeRelease.BaseKey produces
// (ADR-736). storage cannot import pkg/state.
const (
	runtimeReleaseBasePrefix = "base/releases/runner-"
	runtimeReleaseBaseSuffix = ".ext4"
)

// detachedFill is one in-flight background fill of a key. done closes when
// the fill has installed the entry or failed; err is set before that.
type detachedFill struct {
	done    chan struct{}
	err     error
	started time.Time
}

// detachedFillKey reports whether a cache miss of key is filled in the
// background instead of under the caller's context. Only immutable
// runtime-release bases qualify: their bytes never change under a key, so a
// fill nobody waits for any more can never install stale content, and they
// are large enough that one slow-store fill outlasts a cold boot's wake
// deadline (ADR-20261121220000).
func detachedFillKey(key string) bool {
	return strings.HasPrefix(key, runtimeReleaseBasePrefix) && strings.HasSuffix(key, runtimeReleaseBaseSuffix)
}

// fetchDetached serves a cache miss of an immutable key from one background
// fill per key. The fill reads the parent under its own bounded context, so
// a caller whose deadline passes stops waiting but the fill still installs
// the entry, and the caller's retry joins the same fill or hits the cache
// instead of starting the download again.
func (c *LocalCacheBackend) fetchDetached(ctx context.Context, key, parentKey string) (io.ReadCloser, error) {
	fill := c.joinDetachedFill(ctx, key, parentKey)
	select {
	case <-fill.done:
	case <-ctx.Done():
		slog.Default().Info("storage cache: caller stopped waiting; fill continues in the background",
			"key", key, "waited_ms", time.Since(fill.started).Milliseconds(), "err", ctx.Err())
		return nil, fmt.Errorf("storage: cache: get %q: waiting for background fill: %w", key, ctx.Err())
	}
	if fill.err != nil {
		if c.serveStale() {
			if cached, ok := c.openCache(key); ok {
				c.notifyStaleFallback()
				return cached, nil
			}
		}
		return nil, fmt.Errorf("storage: cache: get %q: %w", key, fill.err)
	}
	if cached, ok := c.openCache(key); ok {
		return cached, nil
	}
	// The fill succeeded but left no entry: the blob exceeds the cache
	// budget, or eviction took it before this caller opened it. Read it
	// the attached way, as every other key is.
	return c.fetchFromParent(ctx, key, parentKey, false)
}

// joinDetachedFill returns key's in-flight fill, starting one if there is
// none.
func (c *LocalCacheBackend) joinDetachedFill(ctx context.Context, key, parentKey string) *detachedFill {
	c.fillsMu.Lock()
	defer c.fillsMu.Unlock()
	if fill, ok := c.fills[key]; ok {
		return fill
	}
	if c.fills == nil {
		c.fills = map[string]*detachedFill{}
	}
	fill := &detachedFill{done: make(chan struct{}), started: time.Now()}
	c.fills[key] = fill
	timeout := c.detachedFillTimeout
	if timeout <= 0 {
		timeout = DefaultDetachedFillTimeout
	}
	// WithoutCancel keeps the caller's values (trace, logging) but not its
	// deadline or cancellation; the timeout is the fill's own bound.
	fillCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	c.publishes.Add(1)
	go func() {
		defer c.publishes.Done()
		defer cancel()
		err := c.fillFromParent(fillCtx, key, parentKey)
		c.fillsMu.Lock()
		delete(c.fills, key)
		fill.err = err
		c.fillsMu.Unlock()
		close(fill.done)
		if err != nil {
			slog.Default().Warn("storage cache: background fill failed", "key", key,
				"duration_ms", time.Since(fill.started).Milliseconds(), "err", err)
			return
		}
		slog.Default().Info("storage cache: background fill installed", "key", key,
			"duration_ms", time.Since(fill.started).Milliseconds())
	}()
	return fill
}

// fillFromParent reads parentKey and installs it durably as key's entry.
func (c *LocalCacheBackend) fillFromParent(ctx context.Context, key, parentKey string) error {
	rc, err := c.parent.Get(ctx, parentKey)
	if err != nil {
		return fmt.Errorf("parent: %w", err)
	}
	defer func() { _ = rc.Close() }()
	installed, err := c.materializeCache(ctx, key, rc, false)
	if err != nil {
		return fmt.Errorf("parent read: %w", err)
	}
	return installed.Close()
}
