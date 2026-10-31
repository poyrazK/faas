package main

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compiled lists are shared by every host entry of their account (ADR-091
// amendment 2026-10-31, round 6). Each host load used to read and compile
// every list its rules reference, so one 10,000-item list under a "*" rule
// was held once per cached host and re-read from Postgres on every host's
// load. The cache follows the owner rule sets: the same TTL, dropped by the
// same invalidations, and a load is kept only if the cache generation did
// not move while it ran.
const (
	edgeRuleListCacheTTL = edgeRuleOwnerSetTTL
	// edgeRuleListCacheMaxItems bounds the items of every cached list
	// together; the lists closest to expiry are dropped first.
	edgeRuleListCacheMaxItems = 1_000_000
)

type edgeRuleListKey struct{ account, name string }

type edgeRuleCompiledList struct {
	list      *api.EdgeRuleList
	items     int
	expiresAt time.Time
}

type edgeRuleCompiledLists struct {
	mu      sync.Mutex
	entries map[edgeRuleListKey]edgeRuleCompiledList
	items   int
}

func newEdgeRuleCompiledLists() *edgeRuleCompiledLists {
	return &edgeRuleCompiledLists{entries: map[edgeRuleListKey]edgeRuleCompiledList{}}
}

func (c *edgeRuleCompiledLists) get(account, name string, now time.Time) (*api.EdgeRuleList, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[edgeRuleListKey{account, name}]
	if !ok || !now.Before(entry.expiresAt) {
		return nil, false
	}
	return entry.list, true
}

// put caches a compiled list unless generation moved since start.
func (c *edgeRuleCompiledLists) put(account, name string, entry edgeRuleCompiledList, start uint64, generation func() uint64) {
	if c == nil || generation == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation() != start {
		return
	}
	key := edgeRuleListKey{account, name}
	c.dropLocked(key)
	c.entries[key] = entry
	c.items += entry.items
	for c.items > edgeRuleListCacheMaxItems && len(c.entries) > 1 {
		var oldest edgeRuleListKey
		found := false
		for k, e := range c.entries {
			if k != key && (!found || e.expiresAt.Before(c.entries[oldest].expiresAt)) {
				oldest, found = k, true
			}
		}
		c.dropLocked(oldest)
	}
}

func (c *edgeRuleCompiledLists) dropLocked(key edgeRuleListKey) {
	if entry, ok := c.entries[key]; ok {
		c.items -= entry.items
		delete(c.entries, key)
	}
}

// drop forgets account's lists; "" forgets every list.
func (c *edgeRuleCompiledLists) drop(account string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if account == "" || key.account == account {
			c.dropLocked(key)
		}
	}
}

// edgeRuleListResolver is the ADR-963 read the host loader needs.
type edgeRuleListResolver interface {
	EdgeRuleListsByName(ctx context.Context, accountID string, names []string) ([]state.EdgeRuleList, error)
}

// resolveEdgeRuleLists attaches the account lists each rule's condition
// references, compiled once per host load. A list missing from the store
// (or a store without lists) leaves the reference unresolved, so the
// condition fails to compile and the rule never matches. A store error
// fails the load, keeping the host's last-known-good rules.
func (g *gatewaydEdgeRules) resolveEdgeRuleLists(ctx context.Context, rules []state.EdgeRule) ([]state.EdgeRule, error) {
	wanted := map[string]map[string]struct{}{}
	for _, r := range rules {
		for _, name := range api.EdgeRuleMatchListRefs(r.Match) {
			if wanted[r.AccountID] == nil {
				wanted[r.AccountID] = map[string]struct{}{}
			}
			wanted[r.AccountID][name] = struct{}{}
		}
	}
	resolver, ok := g.store.(edgeRuleListResolver)
	if len(wanted) == 0 || !ok {
		return rules, nil
	}
	resolved := make(map[string]api.EdgeRuleLists, len(wanted))
	for accountID, set := range wanted {
		compiled, err := g.accountEdgeRuleLists(ctx, resolver, accountID, set)
		if err != nil {
			return nil, err
		}
		resolved[accountID] = compiled
	}
	out := make([]state.EdgeRule, len(rules))
	copy(out, rules)
	for i := range out {
		if out[i].Match != nil {
			out[i].MatchLists = resolved[out[i].AccountID]
		}
	}
	return out, nil
}

// accountEdgeRuleLists returns the compiled lists named in set, reading and
// compiling only those not already cached. A missing list is not cached, so
// creating it takes effect on the next load.
func (g *gatewaydEdgeRules) accountEdgeRuleLists(ctx context.Context, resolver edgeRuleListResolver, accountID string, set map[string]struct{}) (api.EdgeRuleLists, error) {
	now := g.now()
	compiled := make(api.EdgeRuleLists, len(set))
	missing := make([]string, 0, len(set))
	for name := range set {
		if l, ok := g.compiledLists.get(accountID, name, now); ok {
			compiled[name] = l
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return compiled, nil
	}
	sort.Strings(missing)
	var generation func() uint64
	var start uint64
	if g.cache != nil {
		generation = g.cache.Generation
		start = generation()
	}
	lists, err := resolver.EdgeRuleListsByName(ctx, accountID, missing)
	if err != nil {
		return nil, err
	}
	for _, l := range lists {
		c, err := api.CompileEdgeRuleList(l.Kind, l.Items)
		if err != nil {
			slog.Warn("edge rule list does not compile; referencing rules disabled", "list", l.Name, "account", accountID, "err", err)
			continue
		}
		compiled[l.Name] = c
		g.compiledLists.put(accountID, l.Name, edgeRuleCompiledList{list: c, items: len(l.Items), expiresAt: now.Add(edgeRuleListCacheTTL)}, start, generation)
	}
	return compiled, nil
}
