package main

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Owner rule sets (ADR-091 amendment 2026-10-27). With the request's owner
// known, a host's rules are the owner's enabled rules whose match_host covers
// the host plus the serving app's "@app" rules. Read per host, every new
// subdomain under a wildcard rule cost a Postgres read; instead the gateway
// caches each owner's whole enabled rule set and filters it per host. An
// owner with more rules than one set holds keeps the per-host read.
const (
	// edgeRuleOwnerSetMaxRules bounds one account's cached set.
	edgeRuleOwnerSetMaxRules = 1000
	// edgeRuleOwnerSetCacheMaxRules bounds every cached set together; the
	// sets closest to expiry are dropped first.
	edgeRuleOwnerSetCacheMaxRules = 50_000
	// edgeRuleOwnerSetTTL matches the compiled host entry's TTL, so a set
	// never keeps a host's policy staler than the host cache would.
	edgeRuleOwnerSetTTL = 30 * time.Second
)

type edgeRuleOwnerSet struct {
	rules []state.EdgeRule
	// tooLarge records an owner past edgeRuleOwnerSetMaxRules for the TTL,
	// so its hosts go straight to the per-host read.
	tooLarge  bool
	expiresAt time.Time
}

// edgeRuleOwnerSets caches owner rule sets by account ID and coalesces
// concurrent loads of one account.
type edgeRuleOwnerSets struct {
	mu    sync.Mutex
	sets  map[string]*edgeRuleOwnerSet
	rules int // Σ len(set.rules)
	loads map[string]chan struct{}
}

func newEdgeRuleOwnerSets() *edgeRuleOwnerSets {
	return &edgeRuleOwnerSets{sets: map[string]*edgeRuleOwnerSet{}, loads: map[string]chan struct{}{}}
}

// get returns owner's cached set, loading it on a miss. A load is cached only
// if generation() did not move while it ran: an invalidation in between may
// have been for a write the load did not see.
func (c *edgeRuleOwnerSets) get(ctx context.Context, owner string, now func() time.Time, generation func() uint64,
	load func(context.Context) ([]state.EdgeRule, bool, error)) (*edgeRuleOwnerSet, error) {
	for {
		c.mu.Lock()
		if set := c.sets[owner]; set != nil && now().Before(set.expiresAt) {
			c.mu.Unlock()
			return set, nil
		}
		if wait, loading := c.loads[owner]; loading {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wait:
			}
			continue
		}
		done := make(chan struct{})
		c.loads[owner] = done
		c.mu.Unlock()

		start := generation()
		rules, complete, err := load(ctx)
		var set *edgeRuleOwnerSet
		if err == nil {
			set = &edgeRuleOwnerSet{expiresAt: now().Add(edgeRuleOwnerSetTTL), tooLarge: !complete}
			if complete {
				set.rules = rules
			}
		}
		c.mu.Lock()
		delete(c.loads, owner)
		close(done)
		if set != nil && generation() == start {
			c.putLocked(owner, set)
		}
		c.mu.Unlock()
		return set, err
	}
}

func (c *edgeRuleOwnerSets) putLocked(owner string, set *edgeRuleOwnerSet) {
	c.dropLocked(owner)
	c.sets[owner] = set
	c.rules += len(set.rules)
	for c.rules > edgeRuleOwnerSetCacheMaxRules {
		oldest := ""
		for key, candidate := range c.sets {
			if oldest == "" || candidate.expiresAt.Before(c.sets[oldest].expiresAt) {
				oldest = key
			}
		}
		c.dropLocked(oldest)
	}
}

func (c *edgeRuleOwnerSets) dropLocked(owner string) {
	if set, ok := c.sets[owner]; ok {
		c.rules -= len(set.rules)
		delete(c.sets, owner)
	}
}

// drop forgets owner's set; "" forgets every set.
func (c *edgeRuleOwnerSets) drop(owner string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if owner == "" {
		c.sets = map[string]*edgeRuleOwnerSet{}
		c.rules = 0
		return
	}
	c.dropLocked(owner)
}

// ownerStoreRules returns owner's rules covering host from the owner's cached
// set. ok is false when the store cannot list an owner's set or the owner has
// more rules than a set holds; the caller then reads the host's rules itself.
func (g *gatewaydEdgeRules) ownerStoreRules(ctx context.Context, host, owner, appID string) ([]state.EdgeRule, bool, error) {
	lister, ok := g.store.(state.OwnerEdgeRuleSetStore)
	if !ok || g.ownerSets == nil {
		return nil, false, nil
	}
	set, err := g.ownerSets.get(ctx, owner, g.now, g.cache.Generation, func(ctx context.Context) ([]state.EdgeRule, bool, error) {
		return lister.OwnerEdgeRules(ctx, owner, edgeRuleOwnerSetMaxRules)
	})
	if err != nil {
		return nil, false, err
	}
	if set.tooLarge {
		return nil, false, nil
	}
	return state.EdgeRulesCoveringHost(set.rules, host, appID, g.now()), true, nil
}
