package gateway

// Route priority for the warm-capacity queue (ADR-978).
//
// When every routable instance of an app is busy, requests wait in the
// per-app queue in vm_concurrency.go. A request is classified only once it
// has to queue, so an unsaturated app never pays for the lookup. Saved rules
// (or, when none are saved, the app's route-health selectors as critical)
// decide the class; crawlers and link-preview bots that match no rule are
// bulk. Everything else is normal.
//
// The same classes order the cold-wake queue (ADR-20261018181846): a full
// wake queue admits a request that outranks a waiter, and a finished wake
// releases critical waiters first. That path never blocks on a rules read
// except to settle a full queue.

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RoutePrioritySource loads an app's effective priority rules.
type RoutePrioritySource func(ctx context.Context, accountID, appID string) ([]api.RoutePriorityRule, error)

type routePriorityRequestKey struct{}

type routePriorityRequest struct {
	method, path string
	trigger      TriggerClass
	known        bool
}

func withRoutePriorityRequest(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, routePriorityRequestKey{}, routePriorityRequest{method: r.Method, path: r.URL.Path, trigger: ClassifyWakeTrigger(r), known: true})
}

type routePriorityCache struct {
	source  RoutePrioritySource
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]routePriorityEntry
	loading map[string]bool // apps with a background load in flight
}

type routePriorityEntry struct {
	rules   []api.RoutePriorityRule
	expires time.Time
}

// SetRoutePrioritySource enables ADR-978 route priorities. Without it every
// queued request is normal priority and the queue stays FIFO.
func (h *Handler) SetRoutePrioritySource(source RoutePrioritySource) {
	if source == nil {
		h.routePriorities = nil
		return
	}
	h.routePriorities = &routePriorityCache{source: source, now: time.Now, entries: map[string]routePriorityEntry{}, loading: map[string]bool{}}
}

// routePriorityFor classifies the request carried in ctx.
func (h *Handler) routePriorityFor(ctx context.Context, app App) routePriority {
	req, ok := ctx.Value(routePriorityRequestKey{}).(routePriorityRequest)
	if !ok {
		return routePriorityNormal
	}
	var rules []api.RoutePriorityRule
	if h.routePriorities != nil {
		rules = h.routePriorities.rules(ctx, app)
	}
	return classifyRoutePriority(rules, req)
}

// classifyRoutePriority applies the first matching rule; crawlers and
// link-preview bots that match none are bulk, and a request the gateway knows
// nothing about is normal.
func classifyRoutePriority(rules []api.RoutePriorityRule, req routePriorityRequest) routePriority {
	if !req.known {
		return routePriorityNormal
	}
	for _, rule := range rules {
		if rule.Matches(req.method, req.path) {
			if rule.Class == api.RoutePriorityCritical {
				return routePriorityCritical
			}
			return routePriorityBulk
		}
	}
	if req.trigger == TriggerClassCrawler || req.trigger == TriggerClassPreviewBot {
		return routePriorityBulk
	}
	return routePriorityNormal
}

// classifyWake classifies cold-wake waiters (ADR-20261018181846). With wait
// false it never blocks: when the app's rules are not cached it starts a
// background load and reports false, so the caller treats every waiter alike.
func (h *Handler) classifyWake(ctx context.Context, appID, accountID string, reqs []routePriorityRequest, wait bool) ([]routePriority, bool) {
	var rules []api.RoutePriorityRule
	if c := h.routePriorities; c != nil {
		app := App{ID: appID, AccountID: accountID}
		switch cached, ok := c.cached(appID); {
		case ok:
			rules = cached
		case wait:
			rules = c.rules(ctx, app)
		default:
			c.prefetch(ctx, app)
			return nil, false
		}
	}
	out := make([]routePriority, len(reqs))
	for i, req := range reqs {
		out[i] = classifyRoutePriority(rules, req)
	}
	return out, true
}

// cached returns the app's rules when a fresh entry exists.
func (c *routePriorityCache) cached(appID string) ([]api.RoutePriorityRule, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[appID]
	if !ok || !c.now().Before(entry.expires) {
		return nil, false
	}
	return entry.rules, true
}

// prefetch loads the app's rules in the background, once at a time per app,
// so a wake that is still booting has them by the time it finishes. The load
// keeps ctx's values but not its cancellation: the request that started it
// may finish first.
func (c *routePriorityCache) prefetch(ctx context.Context, app App) {
	c.mu.Lock()
	if c.loading[app.ID] {
		c.mu.Unlock()
		return
	}
	c.loading[app.ID] = true
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.RoutePriorityPrefetchTimeout)
		defer cancel()
		c.rules(ctx, app)
		c.mu.Lock()
		delete(c.loading, app.ID)
		c.mu.Unlock()
	}()
}

// rules returns the cached rules for app, reloading after the TTL. A failed
// load counts as no rules until the next reload.
func (c *routePriorityCache) rules(ctx context.Context, app App) []api.RoutePriorityRule {
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[app.ID]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.rules
	}
	rules, err := c.source(ctx, app.AccountID, app.ID)
	if err != nil {
		rules = nil
	}
	c.mu.Lock()
	if len(c.entries) >= api.RoutePriorityMaxApps {
		clear(c.entries)
	}
	c.entries[app.ID] = routePriorityEntry{rules: rules, expires: now.Add(api.RoutePriorityCacheTTL)}
	c.mu.Unlock()
	return rules
}
