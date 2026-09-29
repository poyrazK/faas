// adr: 375
package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSharedRateStoreFailureReturns503BeforeGuest(t *testing.T) {
	for _, scope := range []string{"account", "app"} {
		t.Run(scope, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			central := newFakeCentral()
			central.consumeResult = func() (int, bool, error) { return 0, false, errors.New("store unavailable") }
			h.WithCentralBackend(central)
			if scope == "app" {
				h.accountLimiter = h.accountLimiter.WithNoop()
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "rate_limit_unavailable") || rec.Header().Get("x-faas-rate-limit-scope") != scope {
				t.Fatalf("status=%d scope=%s body=%s", rec.Code, rec.Header().Get("x-faas-rate-limit-scope"), rec.Body.String())
			}
			if backend.admits != 0 {
				t.Fatal("unverified shared allowance reached guest admission")
			}
		})
	}
}

func TestCacheHitConsumesAccountAndAppAllowance(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	central := newFakeCentral()
	central.consumeResult = func() (int, bool, error) { return 8, true, nil }
	h.WithCentralBackend(central)
	cache := NewResponseCache()
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "cached", AccountID: backend.app.AccountID, AppID: backend.app.ID, MaxAgeSeconds: 60}
	seedCacheRule(t, h, backend.host, rule)
	now := time.Now()
	key := CacheKey{AppID: backend.app.ID, RuleID: rule.ID, Method: http.MethodGet, NormalizedPath: "/catalog", VaryHash: hostVaryHash(backend.host)}
	cache.Put(key, http.StatusOK, http.Header{}, []byte("cached"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/catalog", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "cached" || central.consumeCalls.Load() != 2 {
		t.Fatalf("cache accounting: status=%d body=%s shared consumes=%d", rec.Code, rec.Body.String(), central.consumeCalls.Load())
	}
	if backend.admits != 0 {
		t.Fatal("cache hit woke the guest")
	}
}
