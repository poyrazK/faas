package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCrawlerPolicySuppressesKnownNonUserWake(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy string
		ua     string
		code   string
	}{
		{name: "block monitor", policy: "block", ua: "UptimeRobot/2.0", code: "crawler_blocked"},
		{name: "cached crawler miss", policy: "cached", ua: "Googlebot/2.1", code: "crawler_cached_miss"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.app.CrawlerPolicy = tc.policy

			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
			req.Header.Set("User-Agent", tc.ua)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Retry-After"); got != "60" {
				t.Fatalf("Retry-After = %q, want 60", got)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("Content-Type = %q, want application/problem+json", got)
			}
			if !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("body = %s, missing problem code %q", rec.Body.String(), tc.code)
			}
			if got := atomic.LoadInt32(b.Admits()); got != 0 {
				t.Fatalf("admissions = %d, want 0", got)
			}
		})
	}
}
