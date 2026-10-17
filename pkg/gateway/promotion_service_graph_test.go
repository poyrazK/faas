package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeprobe"
)

// Real HTTP handlers exercise edge -> candidate frontend -> exact candidate API.
// Network identity and scheduler wake are injected; this is not native VM evidence.
func TestPromotionServiceGraphProbeRoutesCandidatePeers(t *testing.T) {
	oldAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "previous-api") }))
	defer oldAPI.Close()
	newAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(routeprobe.TokenHeader) != "" {
			t.Error("private entry challenge reached a dependency")
		}
		_, _ = io.WriteString(w, "candidate-api")
	}))
	defer newAPI.Close()
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "api", Endpoints: []ServiceEndpoint{
		{InstanceID: "old-api", NodeID: oldAPI.Listener.Addr().String(), DeploymentID: overrideStableID, Port: 8080},
	}}}
	woke, deny := false, false
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		ResolveCallerIdentity: func(_ context.Context, remote string) (string, string, error) {
			if remote == "candidate-guest:123" {
				return "web", "candidate-web", nil
			}
			return "web", "previous-web", nil
		},
		ResolveEnvironmentBinding: func(ctx context.Context, _, deployment, _ string) (ServiceProxyEnvironmentBinding, bool, bool, error) {
			id, graph := overrideStableID, "previous-graph"
			if deployment == "candidate-web" {
				id, graph = overrideCandidateID, "private-promotion"
			}
			return ServiceProxyEnvironmentBinding{FrozenPolicy: true, Target: ServiceTarget{AppID: "api"},
				TargetDeploymentID: id, ReleaseSetID: graph, AccountID: "account", RequireHTTPS: true}, true, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			t.Error("mutable policy replaced frozen binding authorization")
			return ServiceCaller{}, ErrServiceProxyDenied
		},
		AuthorizeBound: func(context.Context, string, string) (ServiceCaller, error) {
			if deny {
				return ServiceCaller{}, ErrServiceProxyDenied
			}
			return ServiceCaller{AppID: "web", AccountID: "account"}, nil
		},
		WakeDeployment: func(_ context.Context, appID, id string) error {
			if appID != "api" || id != overrideCandidateID {
				t.Fatal("wake selected a sibling revision")
			}
			woke = true
			provider.snapshot.Endpoints = append(provider.snapshot.Endpoints, ServiceEndpoint{InstanceID: "new-api", NodeID: newAPI.Listener.Addr().String(), DeploymentID: overrideCandidateID, Port: 8080})
			return nil
		},
		Forward: func(target Target) http.Handler {
			u, _ := url.Parse("http://" + target.NodeID)
			return httputil.NewSingleHostReverseProxy(u)
		},
	})
	frontend := func(remote string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			r := httptest.NewRequest(http.MethodGet, "https://api.internal/health", nil)
			r.RemoteAddr = remote
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, r)
			w.WriteHeader(rec.Code)
			_, _ = io.Copy(w, rec.Body)
		}))
	}
	stable, candidate := frontend("previous-guest:123"), frontend("candidate-guest:123")
	defer stable.Close()
	defer candidate.Close()
	backend := &fakeBackend{app: App{ID: "web", Slug: "demo", AccountID: "account", Plan: api.PlanPro, MaxConcurrency: 2}, host: "demo.apps.dom", upstream: stable.Listener.Addr().String()}
	backend.AddTarget(Target{NodeID: stable.Listener.Addr().String(), InstanceID: "old-web", DeploymentID: "dep-stable"})
	backend.AddTarget(Target{NodeID: candidate.Listener.Addr().String(), InstanceID: "new-web", DeploymentID: "dep-candidate"})
	edge := NewHandlerWith(&routeProbeBackend{fakeBackend: backend, deploymentID: "dep-candidate", token: "probe-token"}, NewMetrics(), nil)
	request := probeRequest("probe-token")
	rec := httptest.NewRecorder()
	edge.ServeHTTP(rec, request)
	if rec.Code != http.StatusOK || rec.Body.String() != "candidate-api" || !woke {
		t.Fatalf("candidate graph=%d %q woke=%v", rec.Code, rec.Body, woke)
	}
	plain := httptest.NewRequest(http.MethodGet, "http://demo.apps.dom/health", nil)
	plain.Host = "demo.apps.dom"
	rec = httptest.NewRecorder()
	edge.ServeHTTP(rec, plain)
	if rec.Code != http.StatusOK || rec.Body.String() != "previous-api" {
		t.Fatalf("normal traffic entered candidate graph: %d %q", rec.Code, rec.Body)
	}
	deny = true
	rec = httptest.NewRecorder()
	edge.ServeHTTP(rec, probeRequest("probe-token"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("graph probe bypassed tenant authorization: %d %q", rec.Code, rec.Body)
	}
	for _, tc := range []struct{ header, value string }{{api.ReleaseHeader, "11111111-1111-4111-8111-111111111111"}, {api.TargetDeploymentHeader, overrideCandidateID}} {
		r := httptest.NewRequest(http.MethodGet, "https://api.internal/health", nil)
		r.RemoteAddr = "previous-guest:123"
		r.Header.Set(tc.header, tc.value)
		deny = false
		rec = httptest.NewRecorder()
		proxy.ServeHTTP(rec, r)
		if rec.Code != http.StatusConflict {
			t.Fatalf("guest header escaped frozen graph: %s = %d", tc.header, rec.Code)
		}
	}
}
