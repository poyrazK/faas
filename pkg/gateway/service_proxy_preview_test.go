// adr: 168
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// previewProxy forwards to a guest that records the caller-environment headers
// it actually received.
func previewProxy(t *testing.T, m *Metrics, caller ServiceCaller, seen *http.Header) *ServiceProxy {
	t.Helper()
	return NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return caller, nil },
		Metrics:   m,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				*seen = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			})
		},
		EndpointTTL: time.Minute,
		Now:         func() time.Time { return time.Unix(100, 0) },
	})
}

func previewCall(t *testing.T, proxy *ServiceProxy) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	proxy.ServeHTTP(httptest.NewRecorder(), req)
}

// Previews are provisioned one app per PR, so a preview has no sibling copy of
// its dependencies and its internal calls land on production. The target is
// entitled to know that: it may want to skip side effects, tag writes, or
// refuse the call outright.
func TestServiceProxyMarksPreviewToProductionCall(t *testing.T) {
	m := NewMetrics()
	var seen http.Header
	proxy := previewProxy(t, m, ServiceCaller{AppID: "app-preview", PreviewOfSlug: "public-api"}, &seen)

	previewCall(t, proxy)

	if got := seen.Get(ServiceCallerEnvHeader); got != "preview" {
		t.Errorf("%s = %q, want preview", ServiceCallerEnvHeader, got)
	}
	if got := seen.Get(ServiceCallerPreviewOfHeader); got != "public-api" {
		t.Errorf("%s = %q, want public-api", ServiceCallerPreviewOfHeader, got)
	}
	if got := testutil.ToFloat64(m.servicePreviewToProduction); got != 1 {
		t.Errorf("preview_to_production = %v, want 1", got)
	}
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	found := false
	for _, family := range families {
		if family.GetName() == "gateway_service_preview_to_production_total" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("gateway_service_preview_to_production_total is not exposed by the metrics registry")
	}
}

func TestServiceProxyCountsPreviewScopedTargetWithoutProductionLeak(t *testing.T) {
	m := NewMetrics()
	var seen http.Header
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: staticProvider{endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-preview-orders", PreviewScoped: true}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "app-preview-client", PreviewOfSlug: "client"}, nil
		},
		Metrics: m,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			})
		},
	})

	previewCall(t, proxy)

	if seen.Get(ServiceCallerEnvHeader) != servicecallerEnvPreview {
		t.Errorf("%s = %q, want preview", ServiceCallerEnvHeader, seen.Get(ServiceCallerEnvHeader))
	}
	if got := testutil.ToFloat64(m.servicePreviewToPreview); got != 1 {
		t.Errorf("preview_to_preview = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.servicePreviewToProduction); got != 0 {
		t.Errorf("preview_to_production = %v, want 0", got)
	}
}

// A production caller is the ordinary case and must carry nothing new, or
// every guest would have to learn to ignore a header it always receives.
func TestServiceProxyLeavesProductionCallerUnmarked(t *testing.T) {
	m := NewMetrics()
	var seen http.Header
	proxy := previewProxy(t, m, ServiceCaller{AppID: "app-prod"}, &seen)

	previewCall(t, proxy)

	if got := seen.Get(ServiceCallerEnvHeader); got != "" {
		t.Errorf("%s = %q, want it unset for a production caller", ServiceCallerEnvHeader, got)
	}
	if got := seen.Get(ServiceCallerPreviewOfHeader); got != "" {
		t.Errorf("%s = %q, want unset", ServiceCallerPreviewOfHeader, got)
	}
	if got := testutil.ToFloat64(m.servicePreviewToProduction); got != 0 {
		t.Errorf("preview_to_production = %v, want 0", got)
	}
}

// A guest must not be able to forge the marker by sending it inbound: the
// header is platform-owned and set only from the authorizer's verdict.
func TestServiceProxyOverwritesSpoofedCallerEnv(t *testing.T) {
	var seen http.Header
	proxy := previewProxy(t, NewMetrics(), ServiceCaller{AppID: "app-prod"}, &seen)

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.Header.Set(ServiceCallerEnvHeader, "production")
	req.Header.Set(ServiceCallerPreviewOfHeader, "something-else")
	proxy.ServeHTTP(httptest.NewRecorder(), req)

	if got := seen.Get(ServiceCallerEnvHeader); got != "" {
		t.Errorf("%s = %q, want the spoofed value stripped", ServiceCallerEnvHeader, got)
	}
	if got := seen.Get(ServiceCallerPreviewOfHeader); got != "" {
		t.Errorf("%s = %q, want the spoofed value stripped", ServiceCallerPreviewOfHeader, got)
	}
}

// Policy rejection happens before endpoint lookup and wake-up: a denied
// preview must not consume production capacity or reach customer code.
func TestServiceProxyRejectsPreviewDependencyBeforeDiscovery(t *testing.T) {
	m := NewMetrics()
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID:     "app-orders",
		Endpoints: []ServiceEndpoint{{InstanceID: "i", NodeID: "n", Port: 8080}},
	}}
	var wakeCalls atomic.Int32
	var forwardCalls atomic.Int32
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, ErrServiceProxyPreviewProductionDenied
		},
		Wake: func(context.Context, string) error {
			wakeCalls.Add(1)
			return nil
		},
		Metrics: m,
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				forwardCalls.Add(1)
			})
		},
	})

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-preview")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	var problem api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodePreviewProductionDependencyDenied {
		t.Errorf("problem code = %q, want %q", problem.Code, api.CodePreviewProductionDependencyDenied)
	}
	if got := provider.calls.Load(); got != 0 {
		t.Errorf("endpoint lookups = %d, want 0", got)
	}
	if got := wakeCalls.Load(); got != 0 {
		t.Errorf("wake calls = %d, want 0", got)
	}
	if got := forwardCalls.Load(); got != 0 {
		t.Errorf("forward calls = %d, want 0", got)
	}
	if got := testutil.ToFloat64(m.serviceCallTotal.WithLabelValues(string(ServiceCallPreviewDenied))); got != 1 {
		t.Errorf("preview_denied = %v, want 1", got)
	}
}
