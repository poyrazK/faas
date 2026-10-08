// spec: §4.1
package gateway

import (
	"testing"
	"time"
)

func TestEdgeRuleNegativeExpiryAndInvalidation(t *testing.T) {
	c := NewEdgeRuleCache(2)
	now := time.Unix(100, 0)
	c.now = func() time.Time { return now }
	c.Put("empty", &HostEntry{})
	now = now.Add(edgeRuleNegativeTTL - time.Nanosecond)
	if _, hit := c.GetMaintenance("empty"); !hit {
		t.Fatal("negative expired early")
	}
	now = now.Add(time.Nanosecond)
	if _, hit := c.Get("empty"); hit {
		t.Fatal("negative survived expiry")
	}
	c.Put("empty", &HostEntry{})
	c.Reset()
	if _, hit := c.Get("empty"); hit {
		t.Fatal("negative survived rule invalidation")
	}
}
func TestEdgeRuleOldLoadCannotRepopulateAfterReset(t *testing.T) {
	c := NewEdgeRuleCache(2)
	generation := c.Generation()
	c.Reset()
	c.PutIfGeneration("host", &HostEntry{}, generation)
	if _, hit := c.Get("host"); hit {
		t.Fatal("stale empty read repopulated cache")
	}
	c.PutIfGeneration("host", &HostEntry{}, c.Generation())
	if _, hit := c.Get("host"); !hit {
		t.Fatal("current read not cached")
	}
}
func TestEdgeRulePopulatedEntriesHaveBoundedFallbackExpiry(t *testing.T) {
	for name, entry := range map[string]*HostEntry{
		"throttle": {Throttle: []EdgeRuleThrottleResolved{{ID: "t"}}},
		"budget":   {Budget: []EdgeRuleBudgetResolved{{ID: "b"}}},
		"cache":    {Cache: []EdgeRuleCacheResolved{{ID: "c"}}},
	} {
		t.Run(name, func(t *testing.T) {
			c := NewEdgeRuleCache(2)
			now := time.Unix(100, 0)
			c.now = func() time.Time { return now }
			c.Put("host", entry)
			now = now.Add(edgeRuleCacheTTL - time.Nanosecond)
			if _, hit := c.Get("host"); !hit {
				t.Fatal("populated entry expired early")
			}
			now = now.Add(time.Nanosecond)
			if _, hit := c.Get("host"); hit {
				t.Fatal("populated entry survived bounded fallback expiry")
			}
		})
	}
}

// An expired entry is no longer served as current, but stays available as the
// last-known-good set for loaders whose reload fails; Reset still drops it.
func TestEdgeRuleExpiredEntryRemainsLastKnownUntilReset(t *testing.T) {
	c := NewEdgeRuleCache(2)
	now := time.Unix(100, 0)
	c.SetClock(func() time.Time { return now })
	c.Put("host", &HostEntry{IP: []EdgeRuleIPResolved{{ID: "deny"}}})
	now = now.Add(edgeRuleCacheTTL)
	if _, hit := c.GetIP("host"); hit {
		t.Fatal("expired entry served as current")
	}
	last, ok := c.GetLastKnownHost("host")
	if !ok || len(last.IP) != 1 || last.IP[0].ID != "deny" {
		t.Fatalf("GetLastKnownHost = %+v, %v; want the expired deny rule", last, ok)
	}
	c.Reset()
	if _, ok := c.GetLastKnownHost("host"); ok {
		t.Fatal("last-known entry survived rule invalidation")
	}
}
