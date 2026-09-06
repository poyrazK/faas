package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type edgeLoadKey struct {
	host       string
	generation uint64
}

// loadHost collapses simultaneous cache misses without detaching database work
// from its request. A canceled waiter leaves immediately; a canceled or failed
// leader releases its slot so surviving requests can retry with their own budget.
// Generations let requests after invalidation start a fresh read independently.
func (g *gatewaydEdgeRules) loadHost(ctx context.Context, host string) (*gateway.HostEntry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.loadMu.Lock()
		key := edgeLoadKey{host: host, generation: g.cache.Generation()}
		if entry, ok := g.cache.GetHost(host); ok {
			g.loadMu.Unlock()
			return entry, nil
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
		return g.finishHostLoad(ctx, host, key, done)
	}
}

func (g *gatewaydEdgeRules) finishHostLoad(ctx context.Context, host string, key edgeLoadKey, done chan struct{}) (*gateway.HostEntry, error) {
	defer func() { g.loadMu.Lock(); delete(g.loads, key); close(done); g.loadMu.Unlock() }()
	return g.loadHostUncached(ctx, host)
}
