// spec: §4.1
package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestCacheWriter_Stores200 verifies the happy path: a 200
// response with no Set-Cookie and no Cache-Control gets
// captured and committed to the cache.
func TestCacheWriter_Stores200(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, clock)
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{
		ID: "rule-1", PathGlob: "/catalog",
		MaxAgeSeconds: 60, StaleIfErrorSeconds: 300,
	}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	app := App{ID: "app-1", Plan: api.PlanPro}
	key := CacheKey{AppID: app.ID, RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}
	// Stand up the writer chain manually for unit-test clarity.
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.WriteHeader(200)
	_, _ = cw.Write([]byte(`{"items":["a","b","c"]}`))
	stored := cw.finishCacheCapture(cache, key, now)
	if !stored {
		t.Fatalf("expected Put to fire")
	}
	if cache.Len() != 1 {
		t.Errorf("cache.Len = %d, want 1", cache.Len())
	}
}

// TestCacheWriter_Skips304 verifies 304 is not in the cacheable
// status set — caching a Not-Modified would require ETag logic
// (deferred). The store path treats 304 as not-cacheable.
func TestCacheWriter_Skips304(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.WriteHeader(304)
	_, _ = cw.Write([]byte(""))
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if stored {
		t.Fatalf("304 should not be stored (cacheable set excludes it)")
	}
}

// TestCacheWriter_SkipsSetCookie verifies responses carrying
// Set-Cookie are never cached. A cached Set-Cookie would replay
// another caller's session id to a fresh request — a real auth
// leak.
func TestCacheWriter_SkipsSetCookie(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.Header().Set("Set-Cookie", "session=abc")
	cw.WriteHeader(200)
	_, _ = cw.Write([]byte("body"))
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if stored {
		t.Fatalf("Set-Cookie must bypass cache")
	}
}

// TestCacheWriter_SkipsNoStore verifies origin Cache-Control:
// no-store is honoured — the app opted out, even if a
// platform-level rule matched.
func TestCacheWriter_SkipsNoStore(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.Header().Set("Cache-Control", "no-store")
	cw.WriteHeader(200)
	_, _ = cw.Write([]byte("body"))
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if stored {
		t.Fatalf("Cache-Control: no-store must bypass cache")
	}
}

// TestCacheWriter_SkipsPrivate verifies origin Cache-Control:
// private is honoured — the app asked for per-client responses.
func TestCacheWriter_SkipsPrivate(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.Header().Set("Cache-Control", "private, max-age=60")
	cw.WriteHeader(200)
	_, _ = cw.Write([]byte("body"))
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if stored {
		t.Fatalf("Cache-Control: private must bypass cache")
	}
}

// TestCacheWriter_BypassOnOverflow verifies the per-entry byte
// cap causes bypass mode, and the buffer is cleared so a
// later Put cannot store a partial body.
func TestCacheWriter_BypassOnOverflow(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, 16) // tiny cap
	cw.WriteHeader(200)
	_, _ = cw.Write([]byte(strings.Repeat("a", 64)))
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if stored {
		t.Fatalf("overflowing the per-entry cap must skip the Put")
	}
}

// TestCacheWriter_404IsCacheable verifies 404 is in the
// cacheable set. A 404 is a legitimate "no rows" response and
// caching it is a clear win — the next request gets the same
// 404 without waking.
func TestCacheWriter_404IsCacheable(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.WriteHeader(404)
	_, _ = cw.Write([]byte("not found"))
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if !stored {
		t.Fatalf("404 should be cacheable")
	}
}

// TestCacheWriter_EmptyBodyNotStored verifies a WriteHeader
// with no subsequent Write is not stored. A status-only
// response is suspicious (typically a HEAD-shaped response on a
// non-HEAD method) and shouldn't bloat the cache.
func TestCacheWriter_EmptyBodyNotStored(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.WriteHeader(200)
	// No Write.
	stored := cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if stored {
		t.Fatalf("status-only responses (no Write) must not be stored")
	}
}

// TestCacheWriter_HeaderCopyNotAliased verifies the header
// snapshot is independent of the live response header — a
// later Set on the live writer must not retroactively mutate
// the cached body.
func TestCacheWriter_HeaderCopyNotAliased(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.Header().Set("X-Foo", "v1")
	cw.WriteHeader(200)
	_, _ = cw.Write([]byte("body"))
	// Capture and stash.
	cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	// Now mutate the live header — the stored entry must remain v1.
	cw.Header().Set("X-Foo", "v2")
	outcome, entry := cache.Get(CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")})
	if outcome != "fresh" {
		t.Fatalf("expected fresh, got %q", outcome)
	}
	if got := entry.header["X-Foo"]; len(got) == 0 || got[0] != "v1" {
		t.Errorf("cached X-Foo = %v, want [v1] (snapshot must not alias live header)", got)
	}
}

func TestCacheWriter_DropsPerRequestPlatformHeaders(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.Header().Set("X-Faas-Request-Id", "old-request")
	cw.Header().Set("X-Faas-Wake", "restored")
	cw.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte("{}"))
	key := CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}
	cw.finishCacheCapture(cache, key, now)

	_, entry := cache.Get(key)
	if entry == nil {
		t.Fatal("expected cached entry")
	}
	if got := http.Header(entry.header).Get("X-Faas-Request-Id"); got != "" {
		t.Fatalf("cached request id = %q", got)
	}
	if got := http.Header(entry.header).Get("X-Faas-Wake"); got != "" {
		t.Fatalf("cached wake metadata = %q", got)
	}
	if got := http.Header(entry.header).Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want preserved", got)
	}
}

// TestCacheWriter_OnlyOneWriteHeader is a stdlib-contract
// regression: calling WriteHeader twice must not flip the
// status. Mirrors statusRecorder's behaviour.
func TestCacheWriter_OnlyOneWriteHeader(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	cw := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	cw.WriteHeader(200)
	cw.WriteHeader(500) // ignored per stdlib contract
	_, _ = cw.Write([]byte("body"))
	cw.finishCacheCapture(cache, CacheKey{AppID: "a", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")}, now)
	if cw.status != 200 {
		t.Errorf("status = %d, want 200 (double WriteHeader must not flip)", cw.status)
	}
}

// Helper aliasing to keep the test readable.
var _ = http.StatusOK
