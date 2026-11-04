package gateway

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30 (round 5): the in-process response cache is
// one LRU shared by every app on the gateway. One app caching many large
// responses pushed every other app's entries out. An app now holds at most a
// quarter of the cache and evicts its own oldest entries first.
func TestResponseCacheOneAppOverItsShareEvictsItsOwnEntries(t *testing.T) {
	const entry = 512 * 1024
	c := NewResponseCacheWithClock(8*1024*1024, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	key := func(app string, i int) CacheKey {
		return CacheKey{AppID: app, DeploymentID: "dep-" + app, RuleID: "rule", Method: "GET", NormalizedPath: fmt.Sprintf("/p/%d", i)}
	}
	put := func(k CacheKey) {
		t.Helper()
		if !c.Put(k, 200, nil, bytes.Repeat([]byte("x"), entry), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct) {
			t.Fatalf("Put(%v) = false", k)
		}
	}
	// The quiet app's entries are the oldest in the shared LRU.
	put(key("quiet", 0))
	put(key("quiet", 1))
	// The share is 2 MiB: the fifth 512 KiB entry pushes out the noisy
	// app's own oldest entry, although the cache is far from full.
	for i := range 5 {
		put(key("noisy", i))
	}
	for i := range 2 {
		if st, _ := c.Get(key("quiet", i)); st != "fresh" {
			t.Errorf("quiet app entry %d = %q, want fresh (another app's churn must not evict it)", i, st)
		}
	}
	if st, _ := c.Get(key("noisy", 0)); st != "" {
		t.Errorf("noisy app's oldest entry = %q, want evicted", st)
	}
	for i := 1; i < 5; i++ {
		if st, _ := c.Get(key("noisy", i)); st != "fresh" {
			t.Errorf("noisy app entry %d = %q, want fresh", i, st)
		}
	}
	if got, want := c.Bytes(), 6*entry; got != want {
		t.Errorf("Bytes = %d, want %d", got, want)
	}

	c.InvalidateByApp("noisy")
	for i := range 4 {
		put(key("noisy", 10+i))
	}
	if st, _ := c.Get(key("noisy", 10)); st != "fresh" {
		t.Errorf("after the app's invalidation its share is free again; entry = %q, want fresh", st)
	}
}
