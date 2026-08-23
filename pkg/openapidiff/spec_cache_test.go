package openapidiff

// Tests for the in-process LRU spec cache (ADR-126 / issue #975
// item #2). The cache lives in apid process memory; it is the
// fast path for `?source=auto` and the load-bearing layer that
// keeps the dashboard snappy when the customer polls every 30s.
//
// The tests use the test-friendly constructor
// NewSpecCacheWithClock so the TTL window is stepped
// deterministically. The clock closure advances time by the
// amount requested, which lets the TTL expiry test run in
// microseconds rather than 5 minutes.
//
// Review-fix (issue #975 item #2): the cache stores the
// pre-rendered JSON body + source string + annotations count
// so a hit can skip parse + merge + render entirely.

import (
	"bytes"
	"testing"
	"time"
)

// TestSpecCache_PutGet_Hit pins the happy path: a Put followed
// by Get with the same inputs returns the same entry.
func TestSpecCache_PutGet_Hit(t *testing.T) {
	clk := time.Now
	c := NewSpecCacheWithClock(8, time.Minute, clk)
	appID := "app-1"
	docSHA := SumSHA256([]byte("doc"))
	routesSHA := SumSHA256([]byte("routes"))
	rulesSHA := SumSHA256([]byte("rules"))
	body := []byte(`{"openapi":"3.1.0"}`)
	now := clk()
	c.Put(appID, docSHA, routesSHA, rulesSHA, body, "auto", 0, now)
	got, ok := c.Get(appID, docSHA, routesSHA, rulesSHA)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if !bytes.Equal(got.Body, body) {
		t.Errorf("Body mismatch: got %q, want %q", got.Body, body)
	}
	if got.Source != "auto" {
		t.Errorf("Source: got %q, want %q", got.Source, "auto")
	}
	if got.AnnotationsCount != 0 {
		t.Errorf("AnnotationsCount: got %d, want 0", got.AnnotationsCount)
	}
	if !got.GeneratedAt.Equal(now) {
		t.Errorf("GeneratedAt: got %v, want %v", got.GeneratedAt, now)
	}
}

// TestSpecCache_PutGet_DegradedSource pins that the source
// string is recorded verbatim at fill time. A cache hit must
// surface the same string the handler would emit on a fresh
// miss for the same inputs — the degraded-source case
// (review-fix) cannot be recomputed on hit.
func TestSpecCache_PutGet_DegradedSource(t *testing.T) {
	c := NewSpecCacheWithClock(8, time.Minute, time.Now)
	docSHA := SumSHA256([]byte("doc"))
	body := []byte(`{"openapi":"3.1.0"}`)
	c.Put("app-1", docSHA, docSHA, docSHA, body, SourceDegradedRoutes, 3, time.Now())
	got, ok := c.Get("app-1", docSHA, docSHA, docSHA)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Source != SourceDegradedRoutes {
		t.Errorf("Source: got %q, want %q", got.Source, SourceDegradedRoutes)
	}
	if got.AnnotationsCount != 3 {
		t.Errorf("AnnotationsCount: got %d, want 3", got.AnnotationsCount)
	}
}

// TestSpecCache_Get_Miss pins the miss path: a Get for an entry
// not in the cache returns (nil, false).
func TestSpecCache_Get_Miss(t *testing.T) {
	c := NewSpecCacheWithClock(8, time.Minute, time.Now)
	docSHA := SumSHA256([]byte("doc"))
	got, ok := c.Get("nope", docSHA, docSHA, docSHA)
	if ok {
		t.Errorf("expected miss; got %+v", got)
	}
}

// TestSpecCache_Get_DifferentSHA pins the per-input discrimination:
// two cache entries with different docSHAs must not collide.
func TestSpecCache_Get_DifferentSHA(t *testing.T) {
	c := NewSpecCacheWithClock(8, time.Minute, time.Now)
	appID := "app-1"
	body1 := []byte(`{"openapi":"3.1.0","v":1}`)
	body2 := []byte(`{"openapi":"3.1.0","v":2}`)
	c.Put(appID, SumSHA256([]byte("doc1")), SumSHA256([]byte("r")), SumSHA256([]byte("rl")), body1, "auto", 0, time.Now())
	c.Put(appID, SumSHA256([]byte("doc2")), SumSHA256([]byte("r")), SumSHA256([]byte("rl")), body2, "auto", 0, time.Now())
	got1, _ := c.Get(appID, SumSHA256([]byte("doc1")), SumSHA256([]byte("r")), SumSHA256([]byte("rl")))
	got2, _ := c.Get(appID, SumSHA256([]byte("doc2")), SumSHA256([]byte("r")), SumSHA256([]byte("rl")))
	if !bytes.Equal(got1.Body, body1) || !bytes.Equal(got2.Body, body2) {
		t.Errorf("expected distinct bodies per docSHA; got1=%q got2=%q", got1.Body, got2.Body)
	}
}

// TestSpecCache_TTL_Expiry pins the freshness window. After
// the TTL elapses, a Get returns miss and the entry is evicted.
func TestSpecCache_TTL_Expiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clk := func() time.Time { return now }
	c := NewSpecCacheWithClock(8, 5*time.Minute, clk)
	appID := "app-1"
	docSHA := SumSHA256([]byte("doc"))
	c.Put(appID, docSHA, docSHA, docSHA, []byte(`{}`), "auto", 0, now)
	// First Get at t=0: hit.
	if _, ok := c.Get(appID, docSHA, docSHA, docSHA); !ok {
		t.Fatal("expected hit at t=0")
	}
	// Advance 4 min: still within TTL.
	now = now.Add(4 * time.Minute)
	if _, ok := c.Get(appID, docSHA, docSHA, docSHA); !ok {
		t.Fatal("expected hit at t=4min (within 5min TTL)")
	}
	// Advance 2 more min (total 6min > TTL): miss + evict.
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get(appID, docSHA, docSHA, docSHA); ok {
		t.Fatal("expected miss at t=6min (past TTL)")
	}
	if c.Len() != 0 {
		t.Errorf("expected entry to be evicted; Len()=%d", c.Len())
	}
}

// TestSpecCache_LRU_Eviction pins the capacity-bounded LRU
// eviction. When the cache is full, the oldest entry is
// evicted to make room for the new one.
func TestSpecCache_LRU_Eviction(t *testing.T) {
	c := NewSpecCacheWithClock(2, time.Minute, time.Now)
	appID := "app-1"
	docA := SumSHA256([]byte("A"))
	docB := SumSHA256([]byte("B"))
	docC := SumSHA256([]byte("C"))
	now := time.Now()
	c.Put(appID, docA, docA, docA, []byte(`{}`), "auto", 0, now)
	c.Put(appID, docB, docB, docB, []byte(`{}`), "auto", 0, now)
	// Cache is full. Adding docC should evict docA (the oldest).
	c.Put(appID, docC, docC, docC, []byte(`{}`), "auto", 0, now)
	if c.Len() != 2 {
		t.Errorf("Len: got %d, want 2", c.Len())
	}
	if _, ok := c.Get(appID, docA, docA, docA); ok {
		t.Error("expected docA to be evicted")
	}
	if _, ok := c.Get(appID, docB, docB, docB); !ok {
		t.Error("expected docB to still be present")
	}
	if _, ok := c.Get(appID, docC, docC, docC); !ok {
		t.Error("expected docC to be present")
	}
}

// TestSpecCache_LRU_Promotion pins that Get moves the entry to
// the front of the LRU. After a Get on docA, docA becomes the
// freshest; docB becomes the oldest. Adding docC evicts docB,
// not docA.
func TestSpecCache_LRU_Promotion(t *testing.T) {
	c := NewSpecCacheWithClock(2, time.Minute, time.Now)
	appID := "app-1"
	docA := SumSHA256([]byte("A"))
	docB := SumSHA256([]byte("B"))
	docC := SumSHA256([]byte("C"))
	now := time.Now()
	c.Put(appID, docA, docA, docA, []byte(`{}`), "auto", 0, now)
	c.Put(appID, docB, docB, docB, []byte(`{}`), "auto", 0, now)
	// Get docA — promotes it to the front.
	if _, ok := c.Get(appID, docA, docA, docA); !ok {
		t.Fatal("docA missing")
	}
	// Now docB is the oldest. Add docC — docB evicted, docA kept.
	c.Put(appID, docC, docC, docC, []byte(`{}`), "auto", 0, now)
	if _, ok := c.Get(appID, docB, docB, docB); ok {
		t.Error("expected docB to be evicted (LRU oldest after promotion)")
	}
	if _, ok := c.Get(appID, docA, docA, docA); !ok {
		t.Error("expected docA to be retained (LRU freshest after Get)")
	}
}

// TestSpecCache_InvalidateByApp pins the wholesale per-app
// invalidation. Two apps' entries are added; invalidating one
// leaves the other.
func TestSpecCache_InvalidateByApp(t *testing.T) {
	c := NewSpecCacheWithClock(8, time.Minute, time.Now)
	docA := SumSHA256([]byte("A"))
	docB := SumSHA256([]byte("B"))
	now := time.Now()
	c.Put("app-1", docA, docA, docA, []byte(`{}`), "auto", 0, now)
	c.Put("app-2", docB, docB, docB, []byte(`{}`), "auto", 0, now)
	c.InvalidateByApp("app-1")
	if _, ok := c.Get("app-1", docA, docA, docA); ok {
		t.Error("app-1 should be invalidated")
	}
	if _, ok := c.Get("app-2", docB, docB, docB); !ok {
		t.Error("app-2 should be retained")
	}
	if c.Len() != 1 {
		t.Errorf("Len: got %d, want 1", c.Len())
	}
}

// TestSpecCache_InvalidateByApp_NoMatch pins the no-op path:
// invalidating a non-existent appID is a silent no-op.
func TestSpecCache_InvalidateByApp_NoMatch(t *testing.T) {
	c := NewSpecCacheWithClock(8, time.Minute, time.Now)
	docA := SumSHA256([]byte("A"))
	now := time.Now()
	c.Put("app-1", docA, docA, docA, []byte(`{}`), "auto", 0, now)
	c.InvalidateByApp("nope")
	if c.Len() != 1 {
		t.Errorf("Len: got %d, want 1", c.Len())
	}
}

// TestSpecCache_Overwrite pins the in-place update. A Put with
// the same key overwrites the existing entry without growing
// the cache.
func TestSpecCache_Overwrite(t *testing.T) {
	c := NewSpecCacheWithClock(8, time.Minute, time.Now)
	appID := "app-1"
	docSHA := SumSHA256([]byte("doc"))
	now := time.Now()
	c.Put(appID, docSHA, docSHA, docSHA, []byte(`{"v":1}`), "auto", 0, now)
	c.Put(appID, docSHA, docSHA, docSHA, []byte(`{"v":2}`), "auto", 0, now)
	if c.Len() != 1 {
		t.Errorf("Len after overwrite: got %d, want 1", c.Len())
	}
	got, _ := c.Get(appID, docSHA, docSHA, docSHA)
	if !bytes.Equal(got.Body, []byte(`{"v":2}`)) {
		t.Errorf("Body after overwrite: got %q, want {\"v\":2}", got.Body)
	}
}

// TestCacheKey_Stable pins the deterministic key shape. Two
// identical inputs produce the same key; differing by a single
// byte produces a different key.
func TestCacheKey_Stable(t *testing.T) {
	appID := "app-1"
	docA := SumSHA256([]byte("doc"))
	docB := SumSHA256([]byte("DOC"))
	docC := SumSHA256([]byte("doc"))
	if CacheKey(appID, docA, docA, docA) != CacheKey(appID, docC, docC, docC) {
		t.Error("identical inputs produced different keys")
	}
	if CacheKey(appID, docA, docA, docA) == CacheKey(appID, docB, docB, docB) {
		t.Error("differing inputs produced the same key")
	}
}

// TestSumSHA256 pins the helper's byte-exact output. The
// reference value comes from the standard library (computed
// below by hand-tracing sha256 of "abc").
func TestSumSHA256(t *testing.T) {
	got := SumSHA256([]byte("abc"))
	// sha256("abc") = ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
	want := [32]byte{
		0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea,
		0x41, 0x41, 0x40, 0xde, 0x5d, 0xae, 0x22, 0x23,
		0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c,
		0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00, 0x15, 0xad,
	}
	if got != want {
		t.Errorf("SumSHA256(\"abc\"): got %x, want %x", got, want)
	}
}