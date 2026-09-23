package gateway

// adr: 168 — the internal service proxy routes only through authorized service targets.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type affinityServiceProvider struct {
	snapshot           ServiceEndpointsSnapshot
	selectedDeployment string
	wakeDone           *atomic.Bool
}

func (p *affinityServiceProvider) ServiceEndpoints(context.Context, string) (ServiceEndpointsSnapshot, error) {
	if p.wakeDone == nil || p.wakeDone.Load() {
		return p.snapshot, nil
	}
	endpoints := make([]ServiceEndpoint, 0, len(p.snapshot.Endpoints))
	for _, endpoint := range p.snapshot.Endpoints {
		if endpoint.DeploymentID != p.selectedDeployment {
			endpoints = append(endpoints, endpoint)
		}
	}
	return ServiceEndpointsSnapshot{AppID: p.snapshot.AppID, Endpoints: endpoints}, nil
}

func (p *affinityServiceProvider) AffinityDeployment(string, string) (string, bool) {
	return p.selectedDeployment, p.selectedDeployment != ""
}

func newAffinityServiceProxy(t *testing.T, provider ServiceEndpointProvider, cfg func(*ServiceProxyConfig)) (*ServiceProxy, *atomic.Value) {
	t.Helper()
	seen := &atomic.Value{}
	config := ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, nil
		},
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen.Store(struct {
					Target Target
					Key    string
				}{Target: target, Key: r.Header.Get(api.VersionKeyHeader)})
				w.WriteHeader(http.StatusNoContent)
			})
		},
	}
	if cfg != nil {
		cfg(&config)
	}
	return NewServiceProxy(config), seen
}

func affinityServiceRequest(proxy *ServiceProxy, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.Header.Set(api.VersionKeyHeader, key)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec
}

func TestServiceProxyVersionAffinityFiltersEndpointsAndPropagatesKey(t *testing.T) {
	provider := &affinityServiceProvider{
		selectedDeployment: "dep-candidate",
		snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
			{InstanceID: "stable-1", NodeID: "node-a", DeploymentID: "dep-stable", Port: 8080},
			{InstanceID: "candidate-1", NodeID: "node-b", DeploymentID: "dep-candidate", Port: 8080},
		}},
	}
	proxy, seen := newAffinityServiceProxy(t, provider, nil)
	if rec := affinityServiceRequest(proxy, "customer-42"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	got := seen.Load().(struct {
		Target Target
		Key    string
	})
	if got.Target.DeploymentID != "dep-candidate" || got.Target.InstanceID != "candidate-1" {
		t.Fatalf("forwarded target = %+v, want candidate endpoint", got.Target)
	}
	if got.Key != "customer-42" {
		t.Fatalf("forwarded version key = %q, want customer-42", got.Key)
	}
}

func TestServiceProxyVersionAffinityWakesExactColdDeployment(t *testing.T) {
	var wakeDone atomic.Bool
	provider := &affinityServiceProvider{
		selectedDeployment: "dep-candidate",
		wakeDone:           &wakeDone,
		snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
			{InstanceID: "stable-1", NodeID: "node-a", DeploymentID: "dep-stable", Port: 8080},
			{InstanceID: "candidate-1", NodeID: "node-b", DeploymentID: "dep-candidate", Port: 8080},
		}},
	}
	var wokeApp, wokeDeployment atomic.Value
	proxy, seen := newAffinityServiceProxy(t, provider, func(config *ServiceProxyConfig) {
		config.WakeDeployment = func(_ context.Context, appID, deploymentID string) error {
			wokeApp.Store(appID)
			wokeDeployment.Store(deploymentID)
			wakeDone.Store(true)
			return nil
		}
	})
	if rec := affinityServiceRequest(proxy, "customer-42"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if wokeApp.Load() != "app-orders" || wokeDeployment.Load() != "dep-candidate" {
		t.Fatalf("wake target = %v/%v, want app-orders/dep-candidate", wokeApp.Load(), wokeDeployment.Load())
	}
	got := seen.Load().(struct {
		Target Target
		Key    string
	})
	if got.Target.DeploymentID != "dep-candidate" {
		t.Fatalf("post-wake target = %+v, want candidate", got.Target)
	}
}
