// adr: 168
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/circuit"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type serviceProxyProvider struct {
	snapshot ServiceEndpointsSnapshot
	calls    atomic.Int32
}

func (p *serviceProxyProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	p.calls.Add(1)
	return p.snapshot, nil
}

func TestServiceProxyRetriesStaleGETAndCachesLease(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
		{InstanceID: "instance-a", NodeID: "node-a", Port: 8080},
		{InstanceID: "instance-b", NodeID: "node-b", Port: 8081},
	}}}
	now := time.Unix(100, 0)
	var calls atomic.Int32
	var seenInstance atomic.Value
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(_ context.Context, caller, target string) (ServiceCaller, error) {
			if caller != "app-client" || target != "app-orders" {
				return ServiceCaller{}, errors.New("unexpected authorization input")
			}
			return ServiceCaller{AppID: caller}, nil
		},
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				seenInstance.Store(r.Header.Get("X-Faas-Instance"))
				if target.InstanceID == "instance-a" {
					markStaleTarget(r.Context())
					http.Error(w, "stale", http.StatusServiceUnavailable)
					return
				}
				if got := r.URL.Path; got != "/health" {
					t.Errorf("downstream path = %q, want /health", got)
				}
				if got := r.Header.Get(ServiceProxyCallerAppHeader); got != "" {
					t.Errorf("caller header leaked downstream: %q", got)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})
		},
		EndpointTTL: time.Second,
		Now:         func() time.Time { return now },
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Fatalf("request %d = %d %q, want 200 ok", i, rec.Code, rec.Body.String())
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("forward calls = %d, want 3 (stale retry plus second request)", got)
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want one cached lease read", got)
	}
	if got := seenInstance.Load(); got != "instance-b" {
		t.Fatalf("last downstream instance = %v, want instance-b", got)
	}
}

func TestServiceProxyHonorsAggregateRetryBudget(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
		{InstanceID: "instance-a", NodeID: "node-a", Port: 8080},
		{InstanceID: "instance-b", NodeID: "node-b", Port: 8081},
	}}}
	now := time.Unix(100, 0)
	metrics := NewMetrics()
	var calls atomic.Int32
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				markStaleTarget(r.Context())
				http.Error(w, "stale", http.StatusServiceUnavailable)
			})
		},
		Metrics:     metrics,
		Now:         func() time.Time { return now },
		RetryBudget: NewRetryBudget(time.Minute, func() time.Time { return now }),
		// Keep endpoints selectable so this test isolates retry admission
		// rather than the circuit breaker opening on the first failure.
		Breaker: circuit.NewGroup(circuit.DefaultConfig(), func() time.Time { return now }),
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
		proxy.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("forward calls = %d, want first request retried and second capped (2+1)", got)
	}
	if got := labelledCounterValue(t, metrics.Registry(), "gateway_retry_exhausted_total", "reason", RetrySkipAggregate); got != 1 {
		t.Fatalf("aggregate budget exhaustions = %v, want 1", got)
	}
}

func TestServiceProxyRefreshesExpiredLease(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}}}
	now := time.Unix(100, 0)
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		},
		EndpointTTL: time.Second,
		Now:         func() time.Time { return now },
	})
	request := func() {
		req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
		proxy.ServeHTTP(httptest.NewRecorder(), req)
	}
	request()
	now = now.Add(2 * time.Second)
	request()
	if got := provider.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want refresh after lease expiry", got)
	}
}

func TestServiceProxyAuthorizationAndCallerIdentity(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}}}
	var forwarded atomic.Bool
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(_ context.Context, caller, target string) (ServiceCaller, error) {
			if caller == "app-foreign" || target != "app-orders" {
				return ServiceCaller{}, ErrServiceProxyDenied
			}
			return ServiceCaller{AppID: caller}, nil
		},
		ResolveCaller: func(context.Context, string) (string, error) { return "app-client", nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded.Store(true); w.WriteHeader(http.StatusOK) })
		},
	})

	for _, tc := range []struct {
		name   string
		caller string
		want   int
	}{
		{name: "resolved identity without header", want: http.StatusOK},
		{name: "identity mismatch", caller: "app-foreign", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders", nil)
			if tc.caller != "" {
				req.Header.Set(ServiceProxyCallerAppHeader, tc.caller)
			}
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
	if !forwarded.Load() {
		t.Fatal("resolved caller request did not reach downstream")
	}
}

func TestServiceProxyDoesNotRetryPOST(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}, {InstanceID: "instance-b", NodeID: "node-b", Port: 8080}}}}
	var calls atomic.Int32
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				markStaleTarget(r.Context())
				http.Error(w, "stale", http.StatusServiceUnavailable)
			})
		},
	})
	req := httptest.NewRequest(http.MethodPost, "http://gateway/v1/internal/services/orders", strings.NewReader("payload"))
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("forward calls = %d, want no retry for POST", got)
	}
}

func TestServiceProxyAddsManagedBindingSpan(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	providerBackend := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
		AppID:     "app-orders",
		Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}},
	}}
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: providerBackend,
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AccountID: "d6e281f3-f5b2-436c-b4ad-8529a956609c"}, nil
		},
		Forward: func(_ Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if span := oteltrace.SpanFromContext(r.Context()); !span.SpanContext().IsValid() {
					t.Fatal("service forward lost dependency span context")
				}
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})

	rootCtx, root := provider.Tracer("test").Start(context.Background(), "request")
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	propagation.TraceContext{}.Inject(rootCtx, propagation.HeaderCarrier(req.Header))
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	root.End()

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	span := findEndedSpan(t, recorder.Ended(), "service.orders")
	if span.SpanKind() != oteltrace.SpanKindClient {
		t.Fatalf("span kind = %s, want client", span.SpanKind())
	}
	if span.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatalf("span parent = %s, want root %s", span.Parent().SpanID(), root.SpanContext().SpanID())
	}
	wantStrings := map[string]string{
		"gregale.dependency.type":       "managed_binding",
		"gregale.dependency.kind":       "service_proxy",
		retainedSpanAccountIDAttribute:  "d6e281f3-f5b2-436c-b4ad-8529a956609c",
		"gregale.service.name":          "orders",
		"gregale.service.target_app_id": "app-orders",
		"http.request.method":           http.MethodGet,
	}
	for key, want := range wantStrings {
		if got := spanAttribute(span.Attributes(), key); got != attribute.StringValue(want) {
			t.Errorf("attribute %s = %v, want %q", key, got, want)
		}
	}
	if got := spanAttribute(span.Attributes(), "http.response.status_code"); got != attribute.IntValue(http.StatusNoContent) {
		t.Errorf("http.response.status_code = %v, want %d", got, http.StatusNoContent)
	}
}

func TestServiceProxyRecordsFailedStatusOnDependencySpan(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	proxy := NewServiceProxy(ServiceProxyConfig{
		ResolveCaller: func(context.Context, string) (string, error) { return "", nil },
	})
	req := httptest.NewRequest(http.MethodGet, "http://payments.svc.gregale:10080/charge", nil)
	req.Host = "payments.svc.gregale:10080"
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	span := findEndedSpan(t, recorder.Ended(), "service.payments")
	if got := spanAttribute(span.Attributes(), "http.response.status_code"); got != attribute.IntValue(http.StatusForbidden) {
		t.Errorf("http.response.status_code = %v, want %d", got, http.StatusForbidden)
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %s, want Error", span.Status().Code)
	}
}

func findEndedSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("spans = %#v, want %q", spans, name)
	return nil
}

func spanAttribute(attrs []attribute.KeyValue, key string) attribute.Value {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value
		}
	}
	return attribute.Value{}
}

func TestServiceProxyRoutesDNSHostName(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}}}
	var gotPath string
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(_ context.Context, service string) (ServiceTarget, bool, error) {
			if service != "orders" {
				t.Fatalf("service = %q, want orders", service)
			}
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize:     func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
		ResolveCaller: func(context.Context, string) (string, error) { return "app-client", nil },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if got := r.Host; got != "orders.svc.gregale:10080" {
					t.Errorf("downstream host = %q, want original host", got)
				}
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	req := httptest.NewRequest(http.MethodGet, "http://orders.svc.gregale:10080/health", nil)
	req.Host = "orders.svc.gregale:10080"
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || gotPath != "/health" {
		t.Fatalf("response = %d path=%q, want 204 /health", rec.Code, gotPath)
	}
}

func TestParseServiceProxyHostRejectsUnsafeNames(t *testing.T) {
	for _, host := range []string{"svc.gregale", "orders.other", "orders.api.svc.gregale", "-orders.svc.gregale", "orders-.svc.gregale"} {
		if service, ok := parseServiceProxyHost(host); ok {
			t.Errorf("parseServiceProxyHost(%q) = %q, true; want rejection", host, service)
		}
	}
	for _, host := range []string{"orders.svc.gregale", "orders.svc.gregale:10080", "ORDERS.SVC.GREGALE."} {
		if service, ok := parseServiceProxyHost(host); !ok || service != "orders" {
			t.Errorf("parseServiceProxyHost(%q) = %q, %v; want orders, true", host, service, ok)
		}
	}
}

func TestServiceProxyRejectsMalformedPathAndEmptyRegistry(t *testing.T) {
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{},
		Resolve: func(context.Context, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
	})
	for _, path := range []string{"/v1/internal/services", "/v1/internal/services/", "/v1/internal/services//health"} {
		req := httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q status = %d, want 404", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty registry status = %d, want 503", rec.Code)
	}
}
