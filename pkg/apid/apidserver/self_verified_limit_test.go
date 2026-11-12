package apidserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Consumer billing keys pick their own rate-limit prefix, so a client cycling
// through made-up keys was limited per key only. A client IP's refused
// requests now share one budget, past which it gets 429 without a lookup
// (bug hunt H15).
func TestConsumerBillingRefusalsAreLimitedPerIP(t *testing.T) {
	srv := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	h := srv.Handler()
	call := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/consumer-billing/some-app", nil)
		req.RemoteAddr = ip + ":40000"
		req.Header.Set("Authorization", "Bearer not-a-consumer-key")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.SelfVerifiedRouteFailuresPerIPPerMinute {
		if code := call("203.0.113.9"); code == http.StatusTooManyRequests {
			t.Fatalf("refusal %d = 429, want the route's own refusal", i)
		}
	}
	if code := call("203.0.113.9"); code != http.StatusTooManyRequests {
		t.Fatalf("past the budget = %d, want 429", code)
	}
	if code := call("198.51.100.4"); code == http.StatusTooManyRequests {
		t.Fatal("another client IP was limited")
	}
}
