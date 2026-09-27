package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type countingConsumerAuthStore struct {
	*fakeConsumerAuthStore
	lookups atomic.Int32
}

func (s *countingConsumerAuthStore) ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (ConsumerAuthKey, error) {
	s.lookups.Add(1)
	return s.fakeConsumerAuthStore.ConsumerKeyByAppAndPrefix(ctx, accountID, appID, prefix)
}

func TestPreAuthRateLimitBlocksBeforeCredentialLookupAndWake(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.ConsumerAuthMode = api.ConsumerAuthModeRequired
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 1, Burst: 1}
	store, token := consumerAuthFixture()
	counted := &countingConsumerAuthStore{fakeConsumerAuthStore: store}
	h.WithConsumerAuth(counted)
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }

	request := func(source string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Forwarded-For", source)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d: %s", rec.Code, rec.Body.String())
	}
	if got := counted.lookups.Load(); got != 1 {
		t.Fatalf("credential lookups = %d, want 1", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("first request wakes = %d, want 1", got)
	}
	b.mu.Lock()
	b.running = false
	b.targets = nil
	b.mu.Unlock()

	rec := request("192.0.2.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" || rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth" {
		t.Fatalf("over-limit response = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
	if got := counted.lookups.Load(); got != 1 {
		t.Fatalf("over-limit request reached credential lookup: %d", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("over-limit request woke VM: %d", got)
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "blocked")); got != 1 {
		t.Fatalf("blocked metric = %v, want 1", got)
	}

	if rec := request("192.0.2.2"); rec.Code != http.StatusOK {
		t.Fatalf("independent source = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPreAuthRateLimitObserveAndUntrustedSource(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 1, Burst: 1}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	request := func(xff string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("first observe request = %d", rec.Code)
	}
	if rec := request("192.0.2.1"); rec.Code != http.StatusOK {
		t.Fatalf("would-block request = %d", rec.Code)
	}
	if got := testutil.ToFloat64(h.metrics.preAuthRateLimited.WithLabelValues("app-1", "would_block")); got != 1 {
		t.Fatalf("would-block metric = %v, want 1", got)
	}
	for _, xff := range []string{"", "192.0.2.1, 192.0.2.2", "garbage"} {
		if rec := request(xff); rec.Code != http.StatusOK {
			t.Fatalf("observe mode blocked untrusted XFF %q: %d", xff, rec.Code)
		}
	}
	b.app.PreAuthRateLimit.Mode = api.PreAuthRateLimitEnforce
	for _, xff := range []string{"", "192.0.2.1, 192.0.2.2", "garbage"} {
		if rec := request(xff); rec.Code != http.StatusForbidden {
			t.Fatalf("enforce mode accepted untrusted XFF %q: %d", xff, rec.Code)
		}
	}
	b.app.PreAuthRateLimit = nil
	if rec := request(""); rec.Code != http.StatusOK {
		t.Fatalf("disabled guard changed legacy path: %d", rec.Code)
	}
}

func TestPreAuthRateLimitBoundsSourcesAndPreservesDebt(t *testing.T) {
	l := newPreAuthSourceLimiter()
	fixed := time.Unix(100, 0)
	l.now = func() time.Time { return fixed }
	for i := 0; i < preAuthSourcesPerApp; i++ {
		if !l.Allow("app-1", fmt.Sprintf("2001:db8::%x", i), 1, 1) {
			t.Fatalf("source %d unexpectedly rejected", i)
		}
	}
	if got := len(l.apps["app-1"].sources); got != preAuthSourcesPerApp {
		t.Fatalf("source buckets = %d, want %d", got, preAuthSourcesPerApp)
	}
	if !l.Allow("app-1", "overflow-1", 1, 1) || l.Allow("app-1", "overflow-2", 1, 1) {
		t.Fatal("new sources did not share the overflow bucket")
	}
	if got := len(l.apps["app-1"].sources); got != preAuthSourcesPerApp {
		t.Fatalf("overflow grew source map to %d", got)
	}
	fixed = fixed.Add(time.Second)
	if !l.Allow("app-1", "overflow-2", 1, 1) {
		t.Fatal("refilled old source should be evictable")
	}
	if got := len(l.apps["app-1"].sources); got != preAuthSourcesPerApp {
		t.Fatalf("eviction changed bucket bound to %d", got)
	}
}

func TestPreAuthRateLimitGlobalBucketCap(t *testing.T) {
	l := newPreAuthSourceLimiter()
	l.maxSources = 2
	fixed := time.Unix(100, 0)
	l.now = func() time.Time { return fixed }
	if !l.Allow("app-1", "192.0.2.1", 1, 1) || !l.Allow("app-2", "192.0.2.2", 1, 1) {
		t.Fatal("initial source buckets were denied")
	}
	if !l.Allow("app-3", "192.0.2.3", 1, 1) || l.Allow("app-3", "192.0.2.4", 1, 1) {
		t.Fatal("global cap did not route new sources into app-3 overflow")
	}
	if l.sourceCount != 2 {
		t.Fatalf("global bucket count = %d, want 2", l.sourceCount)
	}
	fixed = fixed.Add(time.Second)
	if !l.Allow("app-3", "192.0.2.4", 1, 1) || l.sourceCount != 2 {
		t.Fatal("refilled global bucket was not safely evicted")
	}
}

func TestPreAuthRateLimitClampsAfterPlanDowngrade(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.Plan = api.PlanFree
	b.app.PreAuthRateLimit = &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 100, Burst: 500}
	fixed := time.Now()
	h.preAuthLimiter.now = func() time.Time { return fixed }
	for i := 0; i < 21; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 20 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("request %d = %d, want %d: %s", i+1, rec.Code, want, rec.Body.String())
		}
		if i == 20 && rec.Header().Get("x-faas-rate-limit-scope") != "pre-auth" {
			t.Fatalf("downgraded app used %q scope, want pre-auth", rec.Header().Get("x-faas-rate-limit-scope"))
		}
	}
}
