// adr: 122

package gateway

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPGBackend_ResponseCacheMetricsRefreshAfterInvalidation(t *testing.T) {
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, time.Now)
	metrics := NewMetrics()
	b := NewPGBackend(nil, nil, nil).WithResponseCache(cache).WithMetrics(metrics)
	now := time.Now().UTC()
	put := func(appID, path, body string) {
		t.Helper()
		if !cache.Put(CacheKey{AppID: appID, NormalizedPath: path}, 200, nil,
			[]byte(body), now.Add(time.Minute), now.Add(2*time.Minute), nil) {
			t.Fatalf("cache.Put(%s, %s) rejected", appID, path)
		}
	}
	setGauges := func() {
		t.Helper()
		metrics.responseCacheBytes.Set(float64(cache.Bytes()))
		metrics.responseCacheEntries.Set(float64(cache.Len()))
	}

	put("app-1", "/products/1", "one")
	put("app-1", "/health", "two")
	put("app-2", "/products/1", "three")
	setGauges()
	if got := testutil.ToFloat64(metrics.responseCacheEntries); got != 3 {
		t.Fatalf("pre-purge entries gauge = %v, want 3", got)
	}

	b.InvalidateResponseCacheByApp("app-1")
	if got := testutil.ToFloat64(metrics.responseCacheEntries); got != 1 {
		t.Fatalf("app purge entries gauge = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.responseCacheBytes); got != 5 {
		t.Fatalf("app purge bytes gauge = %v, want 5", got)
	}

	put("app-2", "/products/2", "four")
	setGauges()
	if err := b.InvalidateResponseCacheByPath("app-2", "/products/*"); err != nil {
		t.Fatalf("path purge: %v", err)
	}
	if got := testutil.ToFloat64(metrics.responseCacheEntries); got != 0 {
		t.Fatalf("path purge entries gauge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.responseCacheBytes); got != 0 {
		t.Fatalf("path purge bytes gauge = %v, want 0", got)
	}

	put("app-3", "/", "five")
	setGauges()
	b.InvalidateResponseCacheAll()
	if got := testutil.ToFloat64(metrics.responseCacheEntries); got != 0 {
		t.Fatalf("global purge entries gauge = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.responseCacheBytes); got != 0 {
		t.Fatalf("global purge bytes gauge = %v, want 0", got)
	}
}
