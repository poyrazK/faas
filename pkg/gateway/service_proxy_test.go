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
		Resolve:  func(context.Context, string) (string, bool, error) { return "app-orders", true, nil },
		Authorize: func(_ context.Context, caller, target string) error {
			if caller != "app-client" || target != "app-orders" {
				return errors.New("unexpected authorization input")
			}
			return nil
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

func TestServiceProxyRefreshesExpiredLease(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}}}}
	now := time.Unix(100, 0)
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider:  provider,
		Resolve:   func(context.Context, string) (string, bool, error) { return "app-orders", true, nil },
		Authorize: func(context.Context, string, string) error { return nil },
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
		Resolve:  func(context.Context, string) (string, bool, error) { return "app-orders", true, nil },
		Authorize: func(_ context.Context, caller, target string) error {
			if caller == "app-foreign" || target != "app-orders" {
				return ErrServiceProxyDenied
			}
			return nil
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
		{name: "missing caller", want: http.StatusUnauthorized},
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
	if forwarded.Load() {
		t.Fatal("denied request reached downstream")
	}
}

func TestServiceProxyDoesNotRetryPOST(t *testing.T) {
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}, {InstanceID: "instance-b", NodeID: "node-b", Port: 8080}}}}
	var calls atomic.Int32
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider:  provider,
		Resolve:   func(context.Context, string) (string, bool, error) { return "app-orders", true, nil },
		Authorize: func(context.Context, string, string) error { return nil },
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

func TestServiceProxyRejectsMalformedPathAndEmptyRegistry(t *testing.T) {
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider:  &serviceProxyProvider{},
		Resolve:   func(context.Context, string) (string, bool, error) { return "app-orders", true, nil },
		Authorize: func(context.Context, string, string) error { return nil },
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
