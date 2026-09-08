package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type fakeServiceEndpointProvider struct {
	snapshot gateway.ServiceEndpointsSnapshot
	err      error
	calls    []string
}

func (f *fakeServiceEndpointProvider) ServiceEndpoints(_ context.Context, appID string) (gateway.ServiceEndpointsSnapshot, error) {
	f.calls = append(f.calls, appID)
	if f.err != nil {
		return gateway.ServiceEndpointsSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestInternalServiceEndpointsHandlerReturnsSortedRegistryProjection(t *testing.T) {
	provider := &fakeServiceEndpointProvider{snapshot: gateway.ServiceEndpointsSnapshot{
		AppID: "app-1",
		Endpoints: []gateway.ServiceEndpoint{
			{InstanceID: "i-2", NodeID: "node-b", Port: 8080},
			{InstanceID: "i-1", NodeID: "node-a", Port: 9090},
		},
	}}
	lookup := gateway.ResolveSlugFn(func(slug string) (string, bool) {
		if slug == "orders" {
			return "app-1", true
		}
		return "", false
	})
	srv := httptest.NewServer(internalServiceEndpointsHandler(provider, lookup, slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/internal/apps/orders/service-endpoints")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body serviceEndpointsResponseJSON
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Slug != "orders" || body.AppID != "app-1" {
		t.Fatalf("identity = slug %q app %q, want orders/app-1", body.Slug, body.AppID)
	}
	if len(body.Endpoints) != 2 || body.Endpoints[0].InstanceID != "i-2" {
		t.Fatalf("endpoints = %+v, want provider snapshot", body.Endpoints)
	}
	if len(provider.calls) != 1 || provider.calls[0] != "app-1" {
		t.Fatalf("provider calls = %v, want [app-1]", provider.calls)
	}
}

func TestInternalServiceEndpointsHandlerUnknownSlug404(t *testing.T) {
	provider := &fakeServiceEndpointProvider{}
	lookup := gateway.ResolveSlugFn(func(string) (string, bool) { return "", false })
	srv := httptest.NewServer(internalServiceEndpointsHandler(provider, lookup, nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/internal/apps/missing/service-endpoints")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if len(provider.calls) != 0 {
		t.Fatalf("provider calls = %v, want none for unknown slug", provider.calls)
	}
}

func TestInternalServiceEndpointsHandlerEmptySetIsJSONArray(t *testing.T) {
	provider := &fakeServiceEndpointProvider{snapshot: gateway.ServiceEndpointsSnapshot{AppID: "app-1", Endpoints: []gateway.ServiceEndpoint{}}}
	lookup := gateway.ResolveSlugFn(func(string) (string, bool) { return "app-1", true })
	srv := httptest.NewServer(internalServiceEndpointsHandler(provider, lookup, nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/internal/apps/orders/service-endpoints")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var body serviceEndpointsResponseJSON
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Endpoints == nil {
		t.Fatalf("endpoints = null, want []")
	}
	if len(body.Endpoints) != 0 {
		t.Fatalf("endpoints = %+v, want empty", body.Endpoints)
	}
}

func TestInternalServiceEndpointsHandlerRegistryError503(t *testing.T) {
	provider := &fakeServiceEndpointProvider{err: errors.New("db down")}
	lookup := gateway.ResolveSlugFn(func(string) (string, bool) { return "app-1", true })
	srv := httptest.NewServer(internalServiceEndpointsHandler(provider, lookup, nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/internal/apps/orders/service-endpoints")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestInternalServiceEndpointsHandlerRejectsMalformedPathAndMethods(t *testing.T) {
	provider := &fakeServiceEndpointProvider{}
	lookup := gateway.ResolveSlugFn(func(string) (string, bool) { return "app-1", true })
	srv := httptest.NewServer(internalServiceEndpointsHandler(provider, lookup, nil))
	defer srv.Close()

	for _, path := range []string{
		"/v1/internal/apps/orders",
		"/v1/internal/apps/orders/service-endpoints/extra",
		"/v1/internal/apps//service-endpoints",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 404 or 400", path, resp.StatusCode)
		}
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/internal/apps/orders/service-endpoints", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", resp.StatusCode)
	}
}
