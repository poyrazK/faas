package fcvm

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/api"
)

// scanRefreshCoalescer shares one canonical re-read of a base's scan sidecar
// between the boots it refuses. During a refusal every wake on every app using
// the base re-checked the canonical copy on its own: an object-store read, a
// file write and an fsync each, all on the cache lock that layer and snapshot
// reads also take (bug hunt O36). Concurrent refusals now share one refresh,
// and its answer stands for api.VmmdScanRefusalRefreshTTL. The zero value is
// ready to use.
type scanRefreshCoalescer struct {
	group singleflight.Group

	mu     sync.Mutex
	recent map[string]scanRefreshResult
}

type scanRefreshResult struct {
	refreshed bool
	at        time.Time
}

// do returns the result of refresh for key, running it at most once per TTL
// however many callers ask.
func (c *scanRefreshCoalescer) do(ctx context.Context, key string, now time.Time, refresh func(context.Context) bool) bool {
	c.mu.Lock()
	if r, ok := c.recent[key]; ok && now.Sub(r.at) < api.VmmdScanRefusalRefreshTTL {
		c.mu.Unlock()
		return r.refreshed
	}
	c.mu.Unlock()
	v, _, _ := c.group.Do(key, func() (any, error) {
		// A caller that waited on the lock above may arrive after the
		// shared refresh finished; reuse it rather than starting another.
		c.mu.Lock()
		if r, ok := c.recent[key]; ok && now.Sub(r.at) < api.VmmdScanRefusalRefreshTTL {
			c.mu.Unlock()
			return r.refreshed, nil
		}
		c.mu.Unlock()
		// The refresh serves every waiter, so one caller's cancellation
		// must not fail it for the rest.
		refreshed := refresh(context.WithoutCancel(ctx))
		c.mu.Lock()
		if c.recent == nil {
			c.recent = map[string]scanRefreshResult{}
		}
		for k, r := range c.recent {
			if now.Sub(r.at) >= api.VmmdScanRefusalRefreshTTL {
				delete(c.recent, k)
			}
		}
		c.recent[key] = scanRefreshResult{refreshed: refreshed, at: now}
		c.mu.Unlock()
		return refreshed, nil
	})
	refreshed, _ := v.(bool)
	return refreshed
}
