// spec: §4.1
package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResponseCache_PathQueryBoundaryIsolation(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	rule := EdgeRuleCacheResolved{ID: "rule-1", MaxAgeSeconds: 60}
	first := CacheKey{AppID: "app-1", RuleID: rule.ID, Method: http.MethodGet,
		NormalizedPath: "/catalog:kind", Query: "x=1"}
	second := first
	second.NormalizedPath, second.Query = "/catalog", "kind:x=1"
	if !cache.Put(first, http.StatusOK, nil, []byte("first resource"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction()) {
		t.Fatal("could not populate first resource")
	}
	if outcome, entry := cache.Get(second); outcome != "" || entry != nil {
		t.Fatal("a different path/query pair received the first resource's response")
	}
	if !cache.Put(second, http.StatusOK, nil, []byte("second resource"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction()) {
		t.Fatal("could not populate second resource")
	}
	for key, want := range map[CacheKey]string{first: "first resource", second: "second resource"} {
		outcome, entry := cache.Get(key)
		if outcome != "fresh" || entry == nil || string(entry.body) != want {
			t.Fatalf("resource %s?%s did not retain its own response", key.NormalizedPath, key.Query)
		}
	}
}

func TestCacheWriter_MultipleCacheControlValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		store  bool
	}{
		{name: "private second", values: []string{"public", "private"}},
		{name: "no-store second", values: []string{"max-age=60", "no-store"}},
		{name: "mixed case third", values: []string{"public", "max-age=60", "No-StOrE"}},
		{name: "public split fields", values: []string{"public", "max-age=60"}, store: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
			rule := EdgeRuleCacheResolved{ID: "rule-1", MaxAgeSeconds: 60}
			live := httptest.NewRecorder()
			rec := newTestStatusRecorder(live)
			writer := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
			for _, value := range tc.values {
				writer.Header().Add("Cache-Control", value)
			}
			if _, err := writer.Write([]byte("origin response")); err != nil {
				t.Fatal(err)
			}
			key := CacheKey{AppID: "app-1", RuleID: rule.ID, Method: http.MethodGet, NormalizedPath: "/catalog"}
			if stored := writer.finishCacheCapture(cache, key, now); stored != tc.store {
				t.Fatalf("stored = %v, want %v for Cache-Control %q", stored, tc.store, tc.values)
			}
			if live.Body.String() != "origin response" || len(live.Header().Values("Cache-Control")) != len(tc.values) {
				t.Fatal("cache eligibility changed the live response")
			}
		})
	}
}

func TestResponseCache_RepeatedVaryRequestHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, common, first, second string
	}{
		{"Accept-Language", "en;q=0.1", "tr", "de"},
		{"Accept-Encoding", "identity;q=0.1", "gzip", "br"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
			h, _, _ := newTestHandler(t)
			h.WithResponseCache(cache)
			rule := EdgeRuleCacheResolved{ID: "rule-1", PathGlob: "/catalog", MaxAgeSeconds: 60, VaryOn: []string{tc.name}}
			seedCacheRule(t, h, "jane-api.apps.dom", rule)
			app := App{ID: "app-1", Plan: api.PlanPro}
			first := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
			first.Header[tc.name] = []string{tc.common, tc.first}
			key := CacheKey{AppID: app.ID, RuleID: rule.ID, Method: first.Method,
				NormalizedPath: first.URL.Path, VaryHash: computeVaryHash(first, rule.VaryOn)}
			cache.Put(key, http.StatusOK, http.Header{"Vary": []string{tc.name}}, []byte(tc.first),
				now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())

			live := httptest.NewRecorder()
			rec := newTestStatusRecorder(live)
			if served, _ := h.applyEdgeRuleCache(rec, first, app, rec); !served || live.Body.String() != tc.first {
				t.Fatal("the original header variant did not hit its response")
			}
			second := first.Clone(first.Context())
			second.Header[tc.name] = []string{tc.common, tc.second}
			live = httptest.NewRecorder()
			rec = newTestStatusRecorder(live)
			if served, _ := h.applyEdgeRuleCache(rec, second, app, rec); served || live.Body.Len() != 0 {
				t.Fatal("a different repeated-header variant received the first variant's response")
			}
		})
	}
}

type cacheSafetyWriteFailure struct {
	http.ResponseWriter
	fail bool
	n    int
	err  error
}

func (w *cacheSafetyWriteFailure) Write(p []byte) (int, error) {
	if w.fail {
		return w.n, w.err
	}
	return w.ResponseWriter.Write(p)
}

func TestCacheWriter_FailedWriteNeverStores(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		err  error
	}{
		{"closed connection", 0, io.ErrClosedPipe},
		{"partial write with error", 2, io.ErrClosedPipe},
		{"short write without error", 2, nil},
		{"full write with error", 4, io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
			rule := EdgeRuleCacheResolved{ID: "rule-1", MaxAgeSeconds: 60}
			live := &cacheSafetyWriteFailure{ResponseWriter: httptest.NewRecorder(), n: tc.n, err: tc.err}
			rec := newTestStatusRecorder(live)
			writer := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
			if _, err := writer.Write([]byte("prefix")); err != nil {
				t.Fatal(err)
			}
			live.fail = true
			if n, err := writer.Write([]byte("body")); n != tc.n || !errors.Is(err, tc.err) {
				t.Fatalf("write result = (%d, %v), want (%d, %v)", n, err, tc.n, tc.err)
			}
			live.fail = false
			if _, err := writer.Write([]byte("later write")); err != nil {
				t.Fatal(err)
			}
			key := CacheKey{AppID: "app-1", RuleID: rule.ID, Method: http.MethodGet, NormalizedPath: "/catalog"}
			if writer.finishCacheCapture(cache, key, now) || cache.Len() != 0 {
				t.Fatal("failed response write populated the cache")
			}
		})
	}
}
