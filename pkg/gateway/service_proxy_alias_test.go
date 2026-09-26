// adr: 269
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceProxyAliasRequiresBindingEvenWithDirectHost(t *testing.T) {
	allowed := false
	resolveCalls := 0
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
			AppID: "app-billing", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}},
		}},
		ResolveCaller: func(context.Context, string) (string, error) { return "app-frontend", nil },
		AllowAlias: func(_ context.Context, caller, service string) (bool, error) {
			if caller != "app-frontend" || service != "billing" {
				t.Fatalf("alias check = %q/%q", caller, service)
			}
			return allowed, nil
		},
		Resolve: func(_ context.Context, caller, service string) (ServiceTarget, bool, error) {
			resolveCalls++
			if caller != "app-frontend" || service != "billing" {
				t.Fatalf("resolve = %q/%q", caller, service)
			}
			return ServiceTarget{AppID: "app-billing"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	call := func(host string) int {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/charge", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := call("billing.internal:10080"); got != http.StatusForbidden || resolveCalls != 0 {
		t.Fatalf("unbound direct alias = %d, resolve calls = %d", got, resolveCalls)
	}
	allowed = true
	if got := call("billing.internal:10080"); got != http.StatusNoContent || resolveCalls != 1 {
		t.Fatalf("bound alias = %d, resolve calls = %d", got, resolveCalls)
	}
	allowed = false
	if got := call("billing.svc.gregale:10080"); got != http.StatusNoContent || resolveCalls != 2 {
		t.Fatalf("legacy host = %d, resolve calls = %d", got, resolveCalls)
	}
}

func TestServiceProxyAliasRejectsLookupFailureAndHostPathMismatch(t *testing.T) {
	proxy := NewServiceProxy(ServiceProxyConfig{
		ResolveCaller: func(context.Context, string) (string, error) { return "app-frontend", nil },
		AllowAlias: func(context.Context, string, string) (bool, error) {
			return false, errors.New("store unavailable")
		},
	})
	req := httptest.NewRequest(http.MethodGet, "http://billing.internal:10080/charge", nil)
	req.Host = "billing.internal:10080"
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup failure = %d, want 503", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "http://billing.internal:10080/v1/internal/services/identity", nil)
	req.Host = "billing.internal:10080"
	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("host/path mismatch = %d, want 404", rec.Code)
	}
}

func TestParseServiceAliasHost(t *testing.T) {
	for _, host := range []string{"billing.internal", "billing.internal:10080", "BILLING.INTERNAL."} {
		if service, ok := parseServiceAliasHost(host); !ok || service != "billing" {
			t.Errorf("parseServiceAliasHost(%q) = %q, %v", host, service, ok)
		}
	}
	for _, host := range []string{"internal", "billing.other.internal", "-billing.internal", "billing-.internal", "billing.svc.gregale"} {
		if service, ok := parseServiceAliasHost(host); ok {
			t.Errorf("parseServiceAliasHost(%q) = %q, true", host, service)
		}
	}
}
