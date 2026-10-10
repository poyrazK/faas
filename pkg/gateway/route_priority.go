package gateway

// Route priority for the warm-capacity queue (ADR-947).
//
// When every routable instance of an app is busy, requests wait in the
// per-app queue in vm_concurrency.go. A request is classified only once it
// has to queue, so an unsaturated app never pays for the lookup. Saved rules
// (or, when none are saved, the app's route-health selectors as critical)
// decide the class; crawlers and link-preview bots that match no rule are
// bulk. Everything else is normal.

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
}

func withRoutePriorityRequest(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, routePriorityRequestKey{}, routePriorityRequest{method: r.Method, path: r.URL.Path, trigger: ClassifyWakeTrigger(r)})
}

type routePriorityCache struct {
	source  RoutePrioritySource
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]routePriorityEntry
}

type routePriorityEntry struct {
	rules   []api.RoutePriorityRule
	expires time.Time
}

// SetRoutePrioritySource enables ADR-947 route priorities. Without it every
// queued request is normal priority and the queue stays FIFO.
func (h *Handler) SetRoutePrioritySource(source RoutePrioritySource) {
	if source == nil {
		h.routePriorities = nil
		return
	}
	h.routePriorities = &routePriorityCache{source: source, now: time.Now, entries: map[string]routePriorityEntry{}}
}

// routePriorityFor classifies the request carried in ctx.
func (h *Handler) routePriorityFor(ctx context.Context, app App) routePriority {
	req, ok := ctx.Value(routePriorityRequestKey{}).(routePriorityRequest)
	if !ok {
		return routePriorityNormal
	}
	if h.routePriorities != nil {
		for _, rule := range h.routePriorities.rules(ctx, app) {
			if rule.Matches(req.method, req.path) {
				if rule.Class == api.RoutePriorityCritical {
					return routePriorityCritical
				}
				return routePriorityBulk
			}
		}
	}
	if req.trigger == TriggerClassCrawler || req.trigger == TriggerClassPreviewBot {
		return routePriorityBulk
	}
	return routePriorityNormal
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
