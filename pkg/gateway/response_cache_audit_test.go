// adr: 091 — response-cache budgets include retained metadata, and acknowledged
// purges fence local/shared publication from requests already in flight.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResponseCacheChargesHeadersKeysAndEmptyEntries(t *testing.T) {
	const limit = 32 * 1024
	cache := NewResponseCacheWithClock(limit, time.Now)
	now := time.Now()
	for i := 0; i < 100; i++ {
		key := responseCacheSampleKey(i)
		key.AppID = "app"
		key.Query = strings.Repeat("q", 1000)
		cache.Put(key, 200, map[string][]string{"X-Large": {strings.Repeat("h", 8192)}}, []byte("x"), now.Add(time.Minute), now.Add(time.Minute), nil)
	}
	if cache.Len() == 0 || cache.Len() >= 100 || cache.Bytes() > limit {
		t.Fatalf("unbounded retained entries: count=%d bytes=%d", cache.Len(), cache.Bytes())
	}
	retained := 0
	for key, el := range cache.data {
		entry := el.Value.(*cacheEntry)
		retained += len(key) + len(entry.body)
		for name, values := range entry.header {
			retained += len(name)
			for _, value := range values {
				retained += len(value)
			}
		}
	}
	if cache.Bytes() < retained {
		t.Fatalf("reported %d bytes below retained key/header/body bytes %d", cache.Bytes(), retained)
	}
	cache.InvalidateAll()
	for i := 0; i < 100; i++ {
		cache.Put(responseCacheSampleKey(i), 200, nil, nil, now.Add(time.Minute), now.Add(time.Minute), nil)
	}
	if cache.Len() == 0 || cache.Len() >= 100 || cache.Bytes() > limit {
		t.Fatalf("empty entries bypass budget: count=%d bytes=%d", cache.Len(), cache.Bytes())
	}
}

func TestCacheCaptureCannotRefillAfterPurge(t *testing.T) {
	for _, kind := range []string{"app", "path", "tag", "all"} {
		t.Run(kind, func(t *testing.T) {
			cache := NewResponseCache()
			key := CacheKey{AppID: "app", RuleID: "rule", Method: "GET", NormalizedPath: "/catalog"}
			rule := &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 3600}
			rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
			writer := newCacheWriter(rec, rec, rule, ResponseCachePerEntryMaxBytes, cache.beginFill())
			writer.Header().Set("Cache-Tag", "catalog")
			_, _ = writer.Write([]byte("old response"))
			var err error
			switch kind {
			case "app":
				err = cache.InvalidateByAppStrict("app")
			case "path":
				err = cache.InvalidateByAppPath("app", "/catalog")
			case "tag":
				err = cache.InvalidateByAppTag("app", "catalog")
			case "all":
				err = cache.InvalidateAllStrict()
			}
			if err != nil {
				t.Fatal(err)
			}
			if writer.finishCacheCapture(cache, key, time.Now()) {
				t.Fatal("old capture republished after purge")
			}
			if state, _ := cache.Get(key); state != "" {
				t.Fatalf("purged entry reappeared: %q", state)
			}
			fresh := newCacheWriter(rec, rec, rule, ResponseCachePerEntryMaxBytes, cache.beginFill())
			_, _ = fresh.Write([]byte("fresh response"))
			if !fresh.finishCacheCapture(cache, key, time.Now()) {
				t.Fatal("new capture could not store after purge")
			}
		})
	}
}

type auditBlockingSharedCache struct {
	*memorySharedResponseCache
	started  chan struct{}
	release  chan struct{}
	blockGet bool
	once     sync.Once
}

func (s *auditBlockingSharedCache) Put(entry *cacheEntry) error {
	if !s.blockGet {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	return s.memorySharedResponseCache.Put(entry)
}

func (s *auditBlockingSharedCache) Get(key CacheKey) (*cacheEntry, error) {
	entry, err := s.memorySharedResponseCache.Get(key)
	if s.blockGet {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	return entry, err
}

func TestPurgeWaitsForSharedPublication(t *testing.T) {
	shared := &auditBlockingSharedCache{memorySharedResponseCache: &memorySharedResponseCache{entries: map[string]*cacheEntry{}}, started: make(chan struct{}), release: make(chan struct{})}
	cache := NewResponseCache().WithSharedStore(shared)
	key := CacheKey{AppID: "app", RuleID: "rule"}
	fill := cache.beginFill()
	putDone := make(chan struct{})
	go func() {
		defer close(putDone)
		cache.putWithDeployment(key, 200, nil, []byte("old"), time.Now().Add(time.Minute), time.Now().Add(time.Minute), time.Now().Add(time.Minute), nil, nil, "", fill)
	}()
	select {
	case <-shared.started:
	case <-time.After(5 * time.Second):
		t.Fatal("shared publication did not start")
	}
	purgeDone := make(chan error, 1)
	go func() { purgeDone <- cache.InvalidateByAppStrict("app") }()
	select {
	case err := <-purgeDone:
		t.Fatalf("purge acknowledged an unfinished publication: %v", err)
	default:
	}
	close(shared.release)
	select {
	case err := <-purgeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("purge did not finish")
	}
	<-putDone
	if state, _ := cache.Get(key); state != "" {
		t.Fatalf("publication survived acknowledged purge: %q", state)
	}
}

func TestSharedReadCannotRehydrateAfterPurge(t *testing.T) {
	shared := &auditBlockingSharedCache{memorySharedResponseCache: &memorySharedResponseCache{entries: map[string]*cacheEntry{}}, started: make(chan struct{}), release: make(chan struct{}), blockGet: true}
	key := CacheKey{AppID: "app", RuleID: "rule"}
	if err := shared.memorySharedResponseCache.Put(&cacheEntry{key: key, body: []byte("old"), freshUntil: time.Now().Add(time.Minute), staleUntil: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	cache := NewResponseCache().WithSharedStore(shared)
	done := make(chan string, 1)
	go func() { state, _ := cache.Get(key); done <- state }()
	select {
	case <-shared.started:
	case <-time.After(5 * time.Second):
		t.Fatal("shared read did not start")
	}
	if err := cache.InvalidateByAppStrict("app"); err != nil {
		t.Fatal(err)
	}
	close(shared.release)
	select {
	case state := <-done:
		if state != "" {
			t.Fatalf("obsolete shared read was served: %q", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shared read did not finish")
	}
	if cache.Len() != 0 {
		t.Fatal("obsolete shared read rehydrated L1")
	}
}

func TestHTTPOriginFillCannotUndoAcknowledgedPurge(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	cache := NewResponseCache()
	h.WithResponseCache(cache)
	seedCacheRule(t, h, b.host, EdgeRuleCacheResolved{ID: "rule", PathGlob: "/catalog", MaxAgeSeconds: 3600})
	calls := 0
	h.proxyByNode = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				if err := cache.InvalidateByAppStrict(b.app.ID); err != nil {
					t.Fatal(err)
				}
				_, _ = w.Write([]byte("old response"))
				return
			}
			_, _ = w.Write([]byte("new response"))
		})
	}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+b.host+"/catalog", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, rec.Code, rec.Body)
		}
		if i == 0 && cache.Len() != 0 {
			t.Fatal("in-flight HTTP response refilled purged cache")
		}
		if i > 0 && rec.Body.String() != "new response" {
			t.Fatalf("request %d replayed obsolete response: %q", i, rec.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("new response did not become a normal cache hit: %d origin calls", calls)
	}
}

func TestCacheFillRetainsGenerationFromBeforePolicyResolution(t *testing.T) {
	cache := NewResponseCache()
	ctx := withResponseCacheFill(context.Background(), cache)
	cache.InvalidateByApp("app")
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	writer := newCacheWriter(rec, rec, &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 3600}, ResponseCachePerEntryMaxBytes, responseCacheFillFrom(ctx, cache))
	_, _ = writer.Write([]byte("old pinned policy"))
	if writer.finishCacheCapture(cache, CacheKey{AppID: "app", RuleID: "rule"}, time.Now()) {
		t.Fatal("a writer created after purge adopted a new generation for its old pinned policy")
	}
}
