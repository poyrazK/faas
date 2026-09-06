package gateway

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// responseCacheTestClock returns a controllable time source so
// tests can advance time deterministically. Each call returns a
// function that the caller reassigns via the returned setter.
func responseCacheTestClock(t *testing.T, start time.Time) (now func() time.Time, advance func(d time.Duration)) {
	t.Helper()
	mu := sync.Mutex{}
	current := start
	now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	advance = func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		current = current.Add(d)
	}
	return now, advance
}

// responseCacheSampleKey returns a deterministic CacheKey
// distinct from any other sample by `i`. Used by table-driven
// tests so per-iteration keys don't collide.
func responseCacheSampleKey(i int) CacheKey {
	return CacheKey{
		AppID:          fmt.Sprintf("app-%d", i),
		DeploymentID:   fmt.Sprintf("dep-%d", i),
		RuleID:         fmt.Sprintf("rule-%d", i),
		Method:         "GET",
		NormalizedPath: fmt.Sprintf("/path/%d", i),
		VaryHash:       [32]byte{byte(i)},
	}
}

// TestResponseCache_NilReceiver_PassThrough pins the nil-cache
// pass-through. Every method on a nil *ResponseCache must be a
// no-op (or a no-op return) so the gateway code path can wire
// the cache through a pointer field that may legitimately be
// nil in unit tests / pre-wiring.
func TestResponseCache_NilReceiver_PassThrough(t *testing.T) {
	var c *ResponseCache
	if state, entry := c.Get(responseCacheSampleKey(0)); state != "" || entry != nil {
		t.Errorf("nil.Get: got (%q, %v), want (\"\", nil)", state, entry)
	}
	if ok := c.Put(responseCacheSampleKey(0), 200, nil, []byte("body"), time.Now(), time.Now(), nil); ok {
		t.Error("nil.Put returned true, want false")
	}
	c.InvalidateByApp("anything")
	c.InvalidateAll()
	if n := c.Len(); n != 0 {
		t.Errorf("nil.Len = %d, want 0", n)
	}
	if n := c.Bytes(); n != 0 {
		t.Errorf("nil.Bytes = %d, want 0", n)
	}
}

// TestResponseCache_PutThenGet_FreshHit pins the canonical
// happy-path: insert an entry, read it back, observe a fresh
// hit. The clock does not advance between Put and Get, so the
// fresh window is intact.
func TestResponseCache_PutThenGet_FreshHit(t *testing.T) {
	start := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	now, _ := responseCacheTestClock(t, start)
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, now)
	k := responseCacheSampleKey(0)
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
	if !c.Put(k, 200, map[string][]string{"Content-Type": {"application/json"}}, []byte(`{"ok":true}`), start.Add(60*time.Second), start.Add(360*time.Second), ruleAct) {
		t.Fatal("Put returned false, want true")
	}
	state, entry := c.Get(k)
	if state != "fresh" {
		t.Errorf("Get state = %q, want fresh", state)
	}
	if entry == nil {
		t.Fatal("Get entry = nil, want non-nil")
	}
	if !bytes.Equal(entry.body, []byte(`{"ok":true}`)) {
		t.Errorf("body = %q, want %q", entry.body, `{"ok":true}`)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	if c.Bytes() != len(entry.body) {
		t.Errorf("Bytes = %d, want %d", c.Bytes(), len(entry.body))
	}
}

// TestResponseCache_GetPastFresh_StaleOnErrorEligible pins the
// state transition: at now == freshUntil, the entry is past
// fresh but inside stale_if_error. The applier checks the
// outcome under the wake-failure gate; on a normal miss the
// entry is NOT served (that's the security property).
func TestResponseCache_GetPastFresh_StaleOnErrorEligible(t *testing.T) {
	start := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	now, advance := responseCacheTestClock(t, start)
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, now)
	k := responseCacheSampleKey(0)
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
	c.Put(k, 200, nil, []byte("body"), start.Add(60*time.Second), start.Add(360*time.Second), ruleAct)
	advance(61 * time.Second) // 1 second past fresh
	state, entry := c.Get(k)
	if state != "stale_if_error_eligible" {
		t.Errorf("Get state = %q, want stale_if_error_eligible", state)
	}
	if entry == nil {
		t.Fatal("Get entry = nil, want non-nil (stale-on-error-eligible entries must still be returned so the applier can decide under the wake-failure gate)")
	}
}

// TestResponseCache_GetPastStale_Expired pins the final state
// transition: at now == staleUntil, the entry is past the
// stale-on-error window and must be dropped. The store's
// byte counter reclaims the headroom immediately.
func TestResponseCache_GetPastStale_Expired(t *testing.T) {
	start := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	now, advance := responseCacheTestClock(t, start)
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, now)
	k := responseCacheSampleKey(0)
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
	c.Put(k, 200, nil, []byte("body"), start.Add(60*time.Second), start.Add(360*time.Second), ruleAct)
	advance(361 * time.Second) // 1 second past stale
	state, entry := c.Get(k)
	if state != "" || entry != nil {
		t.Errorf("Get past stale = (%q, %v), want (\"\", nil)", state, entry)
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0 (expired entries must be dropped on Get)", c.Len())
	}
	if c.Bytes() != 0 {
		t.Errorf("Bytes = %d, want 0 (expired entries must reclaim their headroom immediately)", c.Bytes())
	}
}

// TestResponseCache_PutOverPerEntryCap_Rejected pins the
// per-entry byte cap. A Put with a body > 1 MiB is rejected
// (NOT truncated) so the applier can count
// outcome="store_skipped" against the gateway cache metric.
func TestResponseCache_PutOverPerEntryCap_Rejected(t *testing.T) {
	now := time.Now
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, now)
	k := responseCacheSampleKey(0)
	huge := bytes.Repeat([]byte("x"), ResponseCachePerEntryMaxBytes+1)
	if c.Put(k, 200, nil, huge, time.Now().Add(time.Minute), time.Now().Add(2*time.Minute), nil) {
		t.Fatal("Put of a body larger than the per-entry cap returned true, want false")
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0 (oversize Put must NOT land in the store)", c.Len())
	}
}

// TestResponseCache_PutBeyondMaxBytes_LRUEvicts pins the
// bounded-by-byte-ceiling property. The store evicts the
// least-recently-used entry to make room. The test installs
// three small entries then a fourth one larger than the
// remaining headroom; the oldest must be evicted.
func TestResponseCache_PutBeyondMaxBytes_LRUEvicts(t *testing.T) {
	c := NewResponseCacheWithClock(200, time.Now)
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	now := time.Now()
	// Three entries of 80 bytes each: total 240, ceiling 200.
	// Putting the fourth must evict the LRU (oldest).
	c.Put(responseCacheSampleKey(1), 200, nil, bytes.Repeat([]byte("a"), 80), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	c.Put(responseCacheSampleKey(2), 200, nil, bytes.Repeat([]byte("b"), 80), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	c.Put(responseCacheSampleKey(3), 200, nil, bytes.Repeat([]byte("c"), 80), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	// Total bytes = 240 > 200, so the Put must evict entry 1.
	if c.Bytes() > 200 {
		t.Fatalf("Bytes = %d, want ≤ 200 (LRU eviction must keep the store under the ceiling)", c.Bytes())
	}
	if state, _ := c.Get(responseCacheSampleKey(1)); state != "" {
		t.Errorf("LRU entry should have been evicted; Get returned %q", state)
	}
	// Touch entry 2 to promote it; the next Put should evict 3
	// (the new LRU), not 2.
	c.Get(responseCacheSampleKey(2))
	c.Put(responseCacheSampleKey(4), 200, nil, bytes.Repeat([]byte("d"), 80), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	if state, _ := c.Get(responseCacheSampleKey(2)); state != "fresh" {
		t.Errorf("promoted entry should still be present; Get returned %q", state)
	}
	if state, _ := c.Get(responseCacheSampleKey(3)); state != "" {
		t.Errorf("3 (LRU after promotion of 2) should have been evicted; Get returned %q", state)
	}
}

// TestResponseCache_PutBeyondMaxBytes_BodyTooLarge_Rejected
// pins the boundary condition where the new entry's body is
// larger than the entire maxBytes ceiling. Even after
// evicting everything, the entry cannot fit; the store must
// reject the Put and stay empty (NOT partially populated).
func TestResponseCache_PutBeyondMaxBytes_BodyTooLarge_Rejected(t *testing.T) {
	c := NewResponseCacheWithClock(100, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{}
	if c.Put(responseCacheSampleKey(0), 200, nil, bytes.Repeat([]byte("x"), 200), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct) {
		t.Fatal("Put of a body larger than maxBytes returned true, want false")
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0", c.Len())
	}
	if c.Bytes() != 0 {
		t.Errorf("Bytes = %d, want 0", c.Bytes())
	}
}

// TestResponseCache_InvalidateByApp_DropsOnlyMatching pins the
// per-app invalidation surface: only entries with the matching
// AppID are dropped. The store's other entries remain so a
// neighbouring app does not pay a cold-boot penalty on a
// neighbouring app's rule change.
func TestResponseCache_InvalidateByApp_DropsOnlyMatching(t *testing.T) {
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	c.Put(responseCacheSampleKey(1), 200, nil, []byte("a"), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	c.Put(responseCacheSampleKey(2), 200, nil, []byte("b"), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	c.InvalidateByApp("app-1")
	if state, _ := c.Get(responseCacheSampleKey(1)); state != "" {
		t.Errorf("app-1 entry should be evicted; Get returned %q", state)
	}
	if state, _ := c.Get(responseCacheSampleKey(2)); state != "fresh" {
		t.Errorf("app-2 entry should remain; Get returned %q", state)
	}
}

func TestResponseCache_InvalidateByAppPath(t *testing.T) {
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	keys := []CacheKey{
		{AppID: "app-1", Method: "GET", NormalizedPath: "/products/1"},
		{AppID: "app-1", Method: "GET", NormalizedPath: "/users/1"},
		{AppID: "app-2", Method: "GET", NormalizedPath: "/products/1"},
	}
	for i := range keys {
		keys[i].RuleID = fmt.Sprintf("rule-%d", i)
		if !c.Put(keys[i], 200, nil, []byte("body"), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct) {
			t.Fatalf("Put(%d) failed", i)
		}
	}
	if err := c.InvalidateByAppPath("app-1", "/products/*"); err != nil {
		t.Fatalf("InvalidateByAppPath: %v", err)
	}
	if state, _ := c.Get(keys[0]); state != "" {
		t.Errorf("matching app path state = %q, want miss", state)
	}
	if state, _ := c.Get(keys[1]); state != "fresh" {
		t.Errorf("non-matching app path state = %q, want fresh", state)
	}
	if state, _ := c.Get(keys[2]); state != "fresh" {
		t.Errorf("other app path state = %q, want fresh", state)
	}
	if err := c.InvalidateByAppPath("app-1", "["); err == nil {
		t.Fatal("invalid glob returned nil error")
	}
}

// TestResponseCache_PutOverwrite_SameKey pins the
// double-insert race: if two threads call Put for the same
// key (e.g. two cold-boot threads serving the same path),
// the second Put must replace the first without leaking the
// first's bytes into the running counter.
func TestResponseCache_PutOverwrite_SameKey(t *testing.T) {
	c := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
	now := time.Now()
	k := responseCacheSampleKey(0)
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	c.Put(k, 200, nil, bytes.Repeat([]byte("a"), 200), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	firstBytes := c.Bytes()
	c.Put(k, 200, nil, bytes.Repeat([]byte("b"), 100), now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
	if c.Bytes() != firstBytes-100 {
		t.Errorf("Bytes = %d, want %d (Put on same key must reclaim the prior entry's bytes)", c.Bytes(), firstBytes-100)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	state, entry := c.Get(k)
	if state != "fresh" {
		t.Errorf("Get state = %q, want fresh", state)
	}
	if !bytes.Equal(entry.body, bytes.Repeat([]byte("b"), 100)) {
		t.Errorf("body = %q, want 100 b's", entry.body)
	}
}

// TestResponseCache_ConcurrentPutNoLeak pins the
// race-detector property: a parallel wave of Puts that
// collectively exceeds the byte ceiling must not grow the
// store past the ceiling. The LRU eviction + the running
// byte counter are the load-bearing safety against a memory
// explosion under concurrent cold-boot traffic.
func TestResponseCache_ConcurrentPutNoLeak(t *testing.T) {
	c := NewResponseCacheWithClock(1024, time.Now)
	now := time.Now()
	ruleAct := &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := responseCacheSampleKey(i)
			body := bytes.Repeat([]byte{byte(i)}, 32)
			c.Put(k, 200, nil, body, now.Add(time.Minute), now.Add(2*time.Minute), ruleAct)
		}(i)
	}
	wg.Wait()
	if c.Bytes() > 1024 {
		t.Errorf("Bytes = %d, want ≤ 1024 (concurrent Puts must respect the byte ceiling)", c.Bytes())
	}
}

// TestResponseCache_HashCacheKey_Stable pins the hash helper's
// stability: two calls with the same values produce the same
// digest. A future refactor that re-orders or re-hashes the
// input would silently invalidate the entire cache.
func TestResponseCache_HashCacheKey_Stable(t *testing.T) {
	a := HashCacheKey([]string{"en-US", "fr-FR"})
	b := HashCacheKey([]string{"en-US", "fr-FR"})
	if a != b {
		t.Errorf("HashCacheKey not stable: %x != %x", a, b)
	}
	c := HashCacheKey([]string{"fr-FR", "en-US"})
	if a == c {
		t.Error("HashCacheKey should depend on order (the applier pre-sorts, but the helper must still distinguish for documentation purposes)")
	}
}

// TestResponseCache_PerEntryCap pins the constant so a future
// "let's loosen" refactor is forced to update this test
// (which lists the user impact in its docstring).
func TestResponseCache_PerEntryCap(t *testing.T) {
	if ResponseCachePerEntryMaxBytes != 1*1024*1024 {
		t.Errorf("ResponseCachePerEntryMaxBytes = %d, want 1 MiB; the plan commits to \"bodies ≤ 1 MiB\" and loosening it lets a single misconfigured rule pin the gateway's resident memory", ResponseCachePerEntryMaxBytes)
	}
}

// TestResponseCache_DefaultMaxBytes pins the platform-wide
// ceiling. 64 MiB matches pkg/gateway/routes.go's route-cache
// posture.
func TestResponseCache_DefaultMaxBytes(t *testing.T) {
	if DefaultResponseCacheMaxBytes != 64*1024*1024 {
		t.Errorf("DefaultResponseCacheMaxBytes = %d, want 64 MiB", DefaultResponseCacheMaxBytes)
	}
}
