package gateway

// adr: 570, 091 — a request's pinned edge policy is refused only by an
// invalidation that can reach one of its sets, not by another tenant's rule
// change: every invalidation advances the cache's one generation.

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func pinScopedEntry(t *testing.T, ctx context.Context, key, owner string) context.Context {
	t.Helper()
	entry := &HostEntry{}
	entry.SetOwner(owner)
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(ctx, key, entry)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestEdgeRuleCacheUnchangedSinceScopesToPinnedSets(t *testing.T) {
	appKey := EdgeRuleCacheKey("a.example.com", "app-a")
	hostKey := EdgeRuleCacheKey("a.example.com", "")
	for _, tc := range []struct {
		name       string
		key, owner string
		invalidate func(*EdgeRuleCache)
		unchanged  bool
	}{
		{"another host", appKey, "acct-a", func(c *EdgeRuleCache) { c.InvalidateHosts([]string{"b.example.com"}) }, true},
		{"another account's wildcard", appKey, "acct-a", func(c *EdgeRuleCache) { c.InvalidateAccountHosts("acct-b", "app-b", []string{"*"}) }, true},
		{"another app's @app rule", appKey, "acct-a", func(c *EdgeRuleCache) {
			c.InvalidateAccountHosts("acct-a", "app-b", []string{api.EdgeRuleAppHosts})
		}, true},
		{"any @app rule on a host-only set", hostKey, "", func(c *EdgeRuleCache) {
			c.InvalidateAccountHosts("acct-b", "app-b", []string{api.EdgeRuleAppHosts})
		}, true},
		{"own account's wildcard", appKey, "acct-a", func(c *EdgeRuleCache) { c.InvalidateAccountHosts("acct-a", "app-b", []string{"*"}) }, false},
		{"own app's @app rule", appKey, "acct-a", func(c *EdgeRuleCache) {
			c.InvalidateAccountHosts("acct-a", "app-a", []string{api.EdgeRuleAppHosts})
		}, false},
		{"own host", appKey, "acct-a", func(c *EdgeRuleCache) { c.InvalidateHosts([]string{"a.example.com"}) }, false},
		{"a set of every account and another account's wildcard", hostKey, "", func(c *EdgeRuleCache) {
			c.InvalidateAccountHosts("acct-b", "app-b", []string{"*"})
		}, false},
		{"reset", appKey, "acct-a", func(c *EdgeRuleCache) { c.Reset() }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewEdgeRuleCache(16)
			ctx := pinScopedEntry(t, context.Background(), tc.key, tc.owner)
			since := c.Generation()
			tc.invalidate(c)
			current, unchanged := c.UnchangedSince(ctx, since, "", nil)
			if unchanged != tc.unchanged || current != since+1 {
				t.Fatalf("UnchangedSince = %d, %v; want %d, %v", current, unchanged, since+1, tc.unchanged)
			}
			// The pinned set and a cached one under the same key agree.
			cached := NewEdgeRuleCache(16)
			entry := &HostEntry{}
			entry.SetOwner(tc.owner)
			cached.Put(tc.key, entry)
			tc.invalidate(cached)
			if cached.HasHost(tc.key) != tc.unchanged {
				t.Fatalf("the cache dropped the set = %v, but UnchangedSince = %v", !cached.HasHost(tc.key), tc.unchanged)
			}
		})
	}
}

func TestEdgeRuleCacheUnchangedSinceChecksTheEntryBeingPinned(t *testing.T) {
	c := NewEdgeRuleCache(16)
	key := EdgeRuleCacheKey("a.example.com", "app-a")
	entry := &HostEntry{}
	entry.SetOwner("acct-a")
	since := c.Generation()
	c.InvalidateAccountHosts("acct-a", "app-a", []string{api.EdgeRuleAppHosts})
	if _, unchanged := c.UnchangedSince(context.Background(), since, key, entry); unchanged {
		t.Fatal("a set read before its own app's rule change was pinned")
	}
	since = c.Generation()
	c.InvalidateAccountHosts("acct-b", "app-b", []string{"*"})
	if _, unchanged := c.UnchangedSince(context.Background(), since, key, entry); !unchanged {
		t.Fatal("another account's rule change refused the pin")
	}
}

// A request whose pins span more invalidations than the log keeps fails
// closed, as every request did before the log.
func TestEdgeRuleCacheUnchangedSinceRefusesALostHistory(t *testing.T) {
	c := NewEdgeRuleCache(16)
	ctx := pinScopedEntry(t, context.Background(), EdgeRuleCacheKey("a.example.com", "app-a"), "acct-a")
	since := c.Generation()
	for range edgeRuleInvalidationLog + 1 {
		c.InvalidateHosts([]string{"b.example.com"})
	}
	if _, unchanged := c.UnchangedSince(ctx, since, "", nil); unchanged {
		t.Fatal("a history older than the log counted as unchanged")
	}
	since = c.Generation()
	c.InvalidateHosts([]string{"b.example.com"})
	if _, unchanged := c.UnchangedSince(ctx, since, "", nil); !unchanged {
		t.Fatal("a logged change to another host refused the pins")
	}
}
