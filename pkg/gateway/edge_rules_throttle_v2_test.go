package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 909 — a composite key combines fields into one identity; a field the
// request lacks makes it missing, an untrusted IP fails closed, and long
// combinations are hashed to a bounded key.
func TestResolveCompositeThrottleKey(t *testing.T) {
	h := &Handler{}
	rule := &EdgeRuleThrottleResolved{KeyBy: api.ThrottleKeyByComposite, KeyFields: []string{"ip", "path", "header:x-tenant"}}
	req := httptest.NewRequest(http.MethodPost, "http://api.example.com/login", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set("X-Tenant", "acme")

	key, ok, unavailable := h.resolveThrottleDimension(req, rule)
	if !ok || unavailable != "" || key != "ip=203.0.113.10\x1fpath=/login\x1fheader:x-tenant=acme" {
		t.Fatalf("composite = (%q, %v, %q)", key, ok, unavailable)
	}

	req.Header.Del("X-Tenant")
	if _, ok, unavailable := h.resolveThrottleDimension(req, rule); ok || unavailable != "" {
		t.Fatalf("missing header: ok=%v unavailable=%q, want missing identity", ok, unavailable)
	}

	req.Header.Set("X-Tenant", "acme")
	req.Header.Add("X-Forwarded-For", "198.51.100.1")
	if _, _, unavailable := h.resolveThrottleDimension(req, rule); unavailable != "caller_ip_untrusted" {
		t.Fatalf("forged XFF: unavailable=%q, want caller_ip_untrusted", unavailable)
	}

	long := compositeThrottleKey([]string{"path=/" + strings.Repeat("a", 300)})
	if !strings.HasPrefix(long, "h:") || len(long) > throttleCompositeMaxKeyBytes {
		t.Fatalf("long key = %q", long)
	}
}

// adr: 909 — with count_statuses the rule admits while its bucket has a
// token and charges only responses it counts: successes never drain the
// bucket, failures do, and once empty the next request is rejected.
func TestEdgeRuleThrottle_CountStatuses(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.edgeRules = stubEdgeRuleMatcher{throttle: &EdgeRuleThrottleResolved{
		ID: "rule-login", AccountID: "acct-1", AppID: "app-1",
		RequestsPerSecond: 0.001, Burst: 2,
		KeyBy: api.ThrottleKeyByIP, MaxKeysPerRule: 100,
		CountStatuses: map[int]bool{http.StatusUnauthorized: true},
	}}
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	attempt := func(status int) bool {
		t.Helper()
		ctx, hooks := withResponseStatusHooks(context.Background())
		req := httptest.NewRequest(http.MethodPost, "http://api.example.com/login", nil).WithContext(ctx)
		req.Header.Set("X-Forwarded-For", "203.0.113.10")
		rec := httptest.NewRecorder()
		if h.applyEdgeRuleThrottle(rec, req, app) {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("rejected with %d, want 429", rec.Code)
			}
			return false
		}
		hooks.run(status)
		return true
	}

	for i := range 5 {
		if !attempt(http.StatusOK) {
			t.Fatalf("successful request %d was throttled; only 401s count", i)
		}
	}
	if !attempt(http.StatusUnauthorized) || !attempt(http.StatusUnauthorized) {
		t.Fatal("the first two failures must be admitted (burst 2)")
	}
	if attempt(http.StatusOK) {
		t.Fatal("after two counted failures the bucket is empty; the next request must be rejected")
	}
}

// Without the per-request hook set (callers outside ServeHTTP) the rule
// falls back to charging every request, never to not charging at all.
func TestEdgeRuleThrottle_CountStatusesWithoutHooksChargesUpfront(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.edgeRules = stubEdgeRuleMatcher{throttle: &EdgeRuleThrottleResolved{
		ID: "rule-x", AccountID: "acct-1", AppID: "app-1", RequestsPerSecond: 0.001, Burst: 1,
		CountStatuses: map[int]bool{http.StatusUnauthorized: true},
	}}
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	if h.applyEdgeRuleThrottle(httptest.NewRecorder(), req, app) {
		t.Fatal("first request rejected")
	}
	if !h.applyEdgeRuleThrottle(httptest.NewRecorder(), req, app) {
		t.Fatal("second request admitted; the fallback must charge upfront")
	}
}

func TestLimiterHasConsumerTokenDoesNotConsume(t *testing.T) {
	l := NewLimiter()
	if !l.HasConsumerToken("rule", "a", 0.001, 1, 1) {
		t.Fatal("fresh consumer must have a token")
	}
	if !l.AllowWithConsumerKey("rule", "a", 0.001, 1, 1) {
		t.Fatal("first charge refused")
	}
	if l.HasConsumerToken("rule", "a", 0.001, 1, 1) {
		t.Fatal("drained consumer reported a token")
	}
	// A new consumer past the cap shares __other__, which is still full.
	if !l.HasConsumerToken("rule", "b", 0.001, 1, 1) {
		t.Fatal("over-cap consumer must see the full __other__ bucket")
	}
	if !l.HasConsumerToken("rule", "b", 0.001, 1, 1) {
		t.Fatal("peeking consumed a token")
	}
}
