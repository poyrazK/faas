package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// edgeRuleLoadRetryBackoff spaces database retries for a host whose last
// read failed. Requests inside the window are answered from the last-known
// rule set (or fail with errEdgeRuleLoadBackoff) without touching Postgres.
const edgeRuleLoadRetryBackoff = 2 * time.Second

var errEdgeRuleLoadBackoff = errors.New("edge rule load backing off after a failed read")

type edgeLoadKey struct {
	host       string
	generation uint64
}

// loadHost collapses simultaneous cache misses without detaching database work
// from its request. A canceled waiter leaves immediately; a canceled or failed
// leader releases its slot so surviving requests can retry with their own budget.
// Generations let requests after invalidation start a fresh read independently.
func (g *gatewaydEdgeRules) loadHost(ctx context.Context, host string) (*gateway.HostEntry, error) {
	// ADR-1191: a host's set includes its app's app-wide rules, so cache,
	// coalescing and backoff are per (host, app).
	cacheKey := gateway.EdgeRuleCacheKey(host, gateway.EdgeRuleApp(ctx))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.loadMu.Lock()
		key := edgeLoadKey{host: cacheKey, generation: g.cache.Generation()}
		if entry, ok := g.cache.GetHost(cacheKey); ok {
			g.loadMu.Unlock()
			return entry, nil
		}
		if until, failed := g.loadFailedUntil[cacheKey]; failed && g.now().Before(until) {
			// A read for this host just failed: answer from the last-known
			// set (or the loader error) without another database attempt, so
			// an outage costs one query per host per backoff window rather
			// than one per kind per request.
			g.loadMu.Unlock()
			return g.lastKnownHost(cacheKey, errEdgeRuleLoadBackoff)
		}
		if done, ok := g.loads[key]; ok {
			g.loadMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if g.loads == nil {
			g.loads = make(map[edgeLoadKey]chan struct{})
		}
		done := make(chan struct{})
		g.loads[key] = done
		g.loadMu.Unlock()
		return g.finishHostLoad(ctx, host, cacheKey, key, done)
	}
}

func (g *gatewaydEdgeRules) finishHostLoad(ctx context.Context, host, cacheKey string, key edgeLoadKey, done chan struct{}) (*gateway.HostEntry, error) {
	entry, err := g.loadHostUncached(ctx, host)
	g.loadMu.Lock()
	switch {
	case err == nil:
		delete(g.loadFailedUntil, cacheKey)
	case ctx.Err() == nil:
		// Only a store failure arms the backoff; a request that gave up
		// says nothing about database health.
		if g.loadFailedUntil == nil {
			g.loadFailedUntil = make(map[string]time.Time)
		}
		g.loadFailedUntil[cacheKey] = g.now().Add(edgeRuleLoadRetryBackoff)
	}
	delete(g.loads, key)
	close(done)
	g.loadMu.Unlock()
	if err != nil {
		return g.lastKnownHost(cacheKey, err)
	}
	return entry, nil
}

func (g *gatewaydEdgeRules) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	return time.Now()
}

// lastKnownHost serves the host's last compiled rule set after a failed read.
// Without one the loader error stands, and each kind keeps its own posture
// (kind=jwt fails closed; the rest treat it as no rule).
func (g *gatewaydEdgeRules) lastKnownHost(cacheKey string, loadErr error) (*gateway.HostEntry, error) {
	entry, ok := g.cache.GetLastKnownHost(cacheKey)
	if !ok {
		return nil, loadErr
	}
	if g.log != nil && !errors.Is(loadErr, errEdgeRuleLoadBackoff) {
		g.log.Warn("edge rule load failed; serving last known rules", "host", logsanitize.Field(gateway.EdgeRuleCacheKeyHost(cacheKey)), "err", loadErr)
	}
	return entry, nil
}
