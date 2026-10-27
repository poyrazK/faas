package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failed caller-identity lookup reaches the guest only as a generic 503, and
// its span is dropped before authorization stamps a tenant. On production
// (e2e report Bug 4) every service call from a draining node failed this way
// with nothing recording why; the proxy must log the cause.
func TestServiceProxyLogsCallerIdentityLookupFailure(t *testing.T) {
	var logs bytes.Buffer
	proxy := NewServiceProxy(ServiceProxyConfig{
		Log:     slog.New(slog.NewTextHandler(&logs, nil)),
		Metrics: NewMetrics(),
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			return "", "", errors.New("lookup caller instance: context deadline exceeded")
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			t.Fatal("authorization ran without a caller identity")
			return ServiceCaller{}, nil
		},
		Forward: func(Target) http.Handler { return http.NotFoundHandler() },
	})
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "caller identity is unavailable") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if got := logs.String(); !strings.Contains(got, "service caller identity lookup failed") || !strings.Contains(got, "context deadline exceeded") || !strings.Contains(got, "service=orders") {
		t.Fatalf("identity failure was not logged with its cause: %q", got)
	}
}
