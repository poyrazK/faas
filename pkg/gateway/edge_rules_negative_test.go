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
func TestEdgeRuleLaterKindsAreNotNegativeEntries(t *testing.T) {
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
			now = now.Add(edgeRuleNegativeTTL + time.Second)
			if _, hit := c.Get("host"); !hit {
				t.Fatal("populated later rule kind dropped or expired as empty")
			}
		})
	}
}
