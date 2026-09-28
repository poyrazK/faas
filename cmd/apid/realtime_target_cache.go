package main

import (
	"container/list"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	managedRealtimePublishTargetCacheMaxEntries = 4096
	managedRealtimePublishTargetCacheMaxTargets = 50000
	managedRealtimePublishTargetCacheTTL        = 30 * time.Second
)

type managedRealtimePublishTargetCacheKey struct {
	endpointID string
	channel    string
}

type managedRealtimePublishTargetCacheEntry struct {
	key       managedRealtimePublishTargetCacheKey
	view      state.ManagedRealtimeChannelPublishTargetView
	expiresAt time.Time
	weight    int
}

// managedRealtimePublishTargetCache serves only while the database
// invalidation listener is connected. Its epoch prevents a lookup started
// before a notification from repopulating the cache after that notification.
type managedRealtimePublishTargetCache struct {
	mu          sync.Mutex
	entries     map[managedRealtimePublishTargetCacheKey]*list.Element
	recent      list.List
	targetCount int
	epoch       uint64
	listening   bool
}

func newManagedRealtimePublishTargetCache() *managedRealtimePublishTargetCache {
	return &managedRealtimePublishTargetCache{
		entries: make(map[managedRealtimePublishTargetCacheKey]*list.Element),
	}
}

func runManagedRealtimeChannelRouteTargetCacheListener(ctx context.Context, cache *managedRealtimePublishTargetCache, listener state.ManagedRealtimeChannelRouteTargetCacheListener, log *slog.Logger) {
	events := listener.WatchManagedRealtimeChannelRouteTargetChanges(ctx)
	for {
		select {
		case <-ctx.Done():
			cache.applyListenerEvent(false)
			return
		case event, ok := <-events:
			if !ok {
				cache.applyListenerEvent(false)
				return
			}
			cache.applyListenerEvent(event.Listening)
			if event.Err != nil && ctx.Err() == nil && log != nil {
				log.WarnContext(ctx, "managed realtime route target cache listener unavailable; using database lookups", "error", event.Err)
			}
		}
	}
}

func (c *managedRealtimePublishTargetCache) lookup(key managedRealtimePublishTargetCacheKey) (state.ManagedRealtimeChannelPublishTargetView, string, uint64) {
	if c == nil {
		return state.ManagedRealtimeChannelPublishTargetView{}, "disabled", 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.listening {
		return state.ManagedRealtimeChannelPublishTargetView{}, "disabled", c.epoch
	}
	element := c.entries[key]
	if element == nil {
		return state.ManagedRealtimeChannelPublishTargetView{}, "miss", c.epoch
	}
	entry := element.Value.(*managedRealtimePublishTargetCacheEntry)
	if !time.Now().Before(entry.expiresAt) {
		c.removeLocked(element)
		return state.ManagedRealtimeChannelPublishTargetView{}, "miss", c.epoch
	}
	c.recent.MoveToFront(element)
	return cloneManagedRealtimeChannelPublishTargetView(entry.view), "hit", c.epoch
}

func (c *managedRealtimePublishTargetCache) store(key managedRealtimePublishTargetCacheKey, epoch uint64, view state.ManagedRealtimeChannelPublishTargetView) {
	if c == nil || len(view.Targets) > managedRealtimePublishTargetCacheMaxTargets {
		return
	}
	weight := len(view.Targets)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.listening || c.epoch != epoch {
		return
	}
	if current := c.entries[key]; current != nil {
		c.removeLocked(current)
	}
	for len(c.entries) >= managedRealtimePublishTargetCacheMaxEntries || c.targetCount+weight > managedRealtimePublishTargetCacheMaxTargets {
		oldest := c.recent.Back()
		if oldest == nil {
			return
		}
		c.removeLocked(oldest)
	}
	entry := &managedRealtimePublishTargetCacheEntry{
		key:       key,
		view:      cloneManagedRealtimeChannelPublishTargetView(view),
		expiresAt: time.Now().Add(managedRealtimePublishTargetCacheTTL),
		weight:    weight,
	}
	element := c.recent.PushFront(entry)
	c.entries[key] = element
	c.targetCount += weight
}

func (c *managedRealtimePublishTargetCache) applyListenerEvent(listening bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	c.listening = listening
	c.entries = make(map[managedRealtimePublishTargetCacheKey]*list.Element)
	c.recent.Init()
	c.targetCount = 0
}

func (c *managedRealtimePublishTargetCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	c.entries = make(map[managedRealtimePublishTargetCacheKey]*list.Element)
	c.recent.Init()
	c.targetCount = 0
}

func (c *managedRealtimePublishTargetCache) removeLocked(element *list.Element) {
	entry := element.Value.(*managedRealtimePublishTargetCacheEntry)
	delete(c.entries, entry.key)
	c.targetCount -= entry.weight
	c.recent.Remove(element)
}

func cloneManagedRealtimeChannelPublishTargetView(view state.ManagedRealtimeChannelPublishTargetView) state.ManagedRealtimeChannelPublishTargetView {
	clone := view
	clone.Targets = make([]state.ManagedRealtimeChannelPublishTarget, len(view.Targets))
	for i, target := range view.Targets {
		clone.Targets[i] = target
		if target.GatewayTargetURL != nil {
			url := *target.GatewayTargetURL
			clone.Targets[i].GatewayTargetURL = &url
		}
	}
	return clone
}
