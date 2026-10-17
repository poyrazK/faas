package gateway

// adr:1543 — managed probes require the original live caller and reviewed release.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceProxyManagedProbePinsReviewedRelease(t *testing.T) {
	deployment, otherDeployment := uuid.NewString(), uuid.NewString()
	release, otherRelease := uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		name        string
		endpoints   []string
		deployments []string
		releases    []string
		status      int
	}{
		{name: "exact replica without selectors", endpoints: []string{deployment}, status: 204},
		{name: "other replica only", endpoints: []string{otherDeployment}, status: 503},
		{name: "unattributed replica only", endpoints: []string{""}, status: 503},
		{name: "parked target", status: 503},
		{name: "mixed replicas", endpoints: []string{otherDeployment, deployment}, status: 204},
		{name: "matching deployment", endpoints: []string{deployment}, deployments: []string{deployment}, status: 204},
		{name: "matching release", endpoints: []string{deployment}, releases: []string{release}, status: 204},
		{name: "matching both selectors", endpoints: []string{deployment}, deployments: []string{deployment}, releases: []string{release}, status: 204},
		{name: "normalized deployment", endpoints: []string{deployment}, deployments: []string{strings.ToUpper(deployment)}, status: 204},
		{name: "normalized release", endpoints: []string{deployment}, releases: []string{strings.ToUpper(release)}, status: 204},
		{name: "different deployment has replica", endpoints: []string{otherDeployment}, deployments: []string{otherDeployment}, status: 409},
		{name: "different deployment despite exact replica", endpoints: []string{deployment}, deployments: []string{otherDeployment}, status: 409},
		{name: "different release", endpoints: []string{deployment}, releases: []string{otherRelease}, status: 409},
		{name: "matching deployment different release", endpoints: []string{deployment}, deployments: []string{deployment}, releases: []string{otherRelease}, status: 409},
		{name: "malformed deployment", endpoints: []string{deployment}, deployments: []string{"invalid"}, status: 400},
		{name: "empty deployment", endpoints: []string{deployment}, deployments: []string{""}, status: 400},
		{name: "duplicate deployment", endpoints: []string{deployment}, deployments: []string{deployment, deployment}, status: 400},
		{name: "joined deployments", endpoints: []string{deployment}, deployments: []string{deployment + "," + otherDeployment}, status: 400},
		{name: "malformed release", endpoints: []string{deployment}, releases: []string{"invalid"}, status: 400},
		{name: "empty release", endpoints: []string{deployment}, releases: []string{""}, status: 400},
		{name: "duplicate release", endpoints: []string{deployment}, releases: []string{release, release}, status: 400},
		{name: "joined releases", endpoints: []string{deployment}, releases: []string{release + "," + otherRelease}, status: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy, provider := managedProbeProxy(t, deployment, release, test.endpoints)
			req := managedProbeRequest()
			if test.deployments != nil {
				req.Header[http.CanonicalHeaderKey(api.TargetDeploymentHeader)] = test.deployments
			}
			if test.releases != nil {
				req.Header[http.CanonicalHeaderKey(api.ReleaseHeader)] = test.releases
			}
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), test.status)
			}
			if test.status == http.StatusNoContent {
				if rec.Header().Get(api.TargetDeploymentHeader) != deployment || rec.Header().Get(api.ReleaseHeader) != release || rec.Header().Get(api.ServiceBindingProbeStageHeader) != "complete" {
					t.Fatalf("probe did not confirm the exact reviewed route: %v", rec.Header())
				}
			} else {
				if rec.Header().Get(api.TargetDeploymentHeader) != "" || rec.Header().Get(api.ReleaseHeader) != "" || rec.Header().Get(api.ServiceBindingProbeStageHeader) == "complete" {
					t.Fatalf("failed probe confirmed route identities: %v", rec.Header())
				}
				if test.status < 500 && provider.calls.Load() != 0 {
					t.Fatal("invalid or conflicting selectors reached the endpoint registry")
				}
			}
		})
	}
}

func TestServiceProxyManagedProbeDoesNotReuseOldRelease(t *testing.T) {
	deployment, release := uuid.NewString(), uuid.NewString()
	proxy, provider := managedProbeProxy(t, deployment, release, []string{deployment})
	// Keep the endpoint cache lease stable while the reviewed graph changes.
	proxy.now = func() time.Time { return time.Unix(100, 0) }
	first := httptest.NewRecorder()
	proxy.ServeHTTP(first, managedProbeRequest())
	if first.Code != http.StatusNoContent || first.Header().Get(api.ReleaseHeader) != release {
		t.Fatalf("initial route: status=%d headers=%v", first.Code, first.Header())
	}
	nextDeployment, nextRelease := uuid.NewString(), uuid.NewString()
	resolve := proxy.resolveEnvironmentBinding
	proxy.resolveEnvironmentBinding = func(ctx context.Context, app, dep, service string) (ServiceProxyEnvironmentBinding, bool, bool, error) {
		binding, managed, found, err := resolve(ctx, app, dep, service)
		binding.TargetDeploymentID, binding.ReleaseSetID = nextDeployment, nextRelease
		return binding, managed, found, err
	}
	for _, test := range []struct {
		name string
		pin  bool
		code int
	}{
		{"cached old replica", false, http.StatusServiceUnavailable},
		{"previous release selector", true, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := managedProbeRequest()
			if test.pin {
				req.Header.Set(api.ReleaseHeader, release)
			}
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != test.code || rec.Header().Get(api.TargetDeploymentHeader) != "" || rec.Header().Get(api.ReleaseHeader) != "" {
				t.Fatalf("old route reused: status=%d headers=%v", rec.Code, rec.Header())
			}
		})
	}
	if provider.calls.Load() != 1 {
		t.Fatal("test did not retain the original cached registry snapshot")
	}
	provider.snapshot.Endpoints[0].DeploymentID = nextDeployment
	proxy.invalidateEndpoints("physical-billing")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, managedProbeRequest())
	if rec.Code != http.StatusNoContent || rec.Header().Get(api.TargetDeploymentHeader) != nextDeployment || rec.Header().Get(api.ReleaseHeader) != nextRelease {
		t.Fatalf("new route not confirmed: status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestServiceProxyManagedProbeFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ServiceProxy, *http.Request)
		status int
	}{
		{"registry unavailable", func(p *ServiceProxy, _ *http.Request) { p.provider = &errProvider{} }, 503},
		{"registry missing", func(p *ServiceProxy, _ *http.Request) { p.provider = nil }, 503},
		{"HTTPS required", func(_ *ServiceProxy, r *http.Request) { r.TLS = nil }, 403},
		{"binding missing", func(p *ServiceProxy, _ *http.Request) {
			p.resolveEnvironmentBinding = func(context.Context, string, string, string) (ServiceProxyEnvironmentBinding, bool, bool, error) {
				return ServiceProxyEnvironmentBinding{}, true, false, nil
			}
		}, 403},
		{"binding unreadable", func(p *ServiceProxy, _ *http.Request) {
			p.resolveEnvironmentBinding = func(context.Context, string, string, string) (ServiceProxyEnvironmentBinding, bool, bool, error) {
				return ServiceProxyEnvironmentBinding{}, true, false, errors.New("unavailable")
			}
		}, 503},
		{"binding incomplete", func(p *ServiceProxy, _ *http.Request) {
			p.resolveEnvironmentBinding = func(context.Context, string, string, string) (ServiceProxyEnvironmentBinding, bool, bool, error) {
				return ServiceProxyEnvironmentBinding{Target: ServiceTarget{AppID: "physical-billing"}}, true, true, nil
			}
		}, 503},
		{"foreign account", func(p *ServiceProxy, _ *http.Request) {
			p.authorize = func(context.Context, string, string) (ServiceCaller, error) {
				return ServiceCaller{AppID: "caller", AccountID: "other-account"}, nil
			}
		}, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			deployment, release := uuid.NewString(), uuid.NewString()
			proxy, _ := managedProbeProxy(t, deployment, release, []string{deployment})
			req := managedProbeRequest()
			test.change(proxy, req)
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != test.status || rec.Header().Get(api.TargetDeploymentHeader) != "" || rec.Header().Get(api.ReleaseHeader) != "" || rec.Header().Get(api.ServiceBindingProbeStageHeader) == "complete" {
				t.Fatalf("unavailable route confirmed: status=%d headers=%v", rec.Code, rec.Header())
			}
		})
	}
}

func managedProbeRequest() *http.Request {
	req := httptest.NewRequest(http.MethodHead, "https://billing.internal"+api.ServiceBindingProbePath, nil)
	req.Header.Set(api.ServiceBindingProbeRequestHeader, api.ServiceBindingProbeVersion)
	return req
}

func managedProbeProxy(t *testing.T, deployment, release string, deployments []string) (*ServiceProxy, *serviceProxyProvider) {
	t.Helper()
	endpoints := make([]ServiceEndpoint, 0, len(deployments))
	for _, id := range deployments {
		endpoints = append(endpoints, ServiceEndpoint{InstanceID: uuid.NewString(), NodeID: "node", Port: 8080, DeploymentID: id})
	}
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "physical-billing", Endpoints: endpoints}}
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		ResolveCallerIdentity: func(context.Context, string) (string, string, error) {
			return "caller", "caller-deployment", nil
		},
		ResolveEnvironmentBinding: func(_ context.Context, app, dep, service string) (ServiceProxyEnvironmentBinding, bool, bool, error) {
			if app != "caller" || dep != "caller-deployment" || service != "billing" {
				t.Fatalf("wrong managed selector: %q %q %q", app, dep, service)
			}
			return ServiceProxyEnvironmentBinding{Target: ServiceTarget{AppID: "physical-billing"}, TargetDeploymentID: deployment, ReleaseSetID: release, AccountID: "account", RequireHTTPS: true}, true, true, nil
		},
		Authorize: func(_ context.Context, caller, target string) (ServiceCaller, error) {
			if caller != "caller" || target != "physical-billing" {
				t.Fatalf("wrong authorization: %q %q", caller, target)
			}
			return ServiceCaller{AppID: caller, AccountID: "account"}, nil
		},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			t.Fatal("managed probe fell back to app-slug resolution")
			return ServiceTarget{}, false, nil
		},
		AllowAlias: func(context.Context, string, string) (bool, error) {
			t.Fatal("managed probe fell back to app alias authorization")
			return false, nil
		},
		ResolveRelease: func(context.Context, string, string, string, string) (string, string, error) {
			t.Fatal("managed probe replaced frozen route with ordinary release resolution")
			return "", "", nil
		},
		ValidateDeployment: func(context.Context, string, string) (bool, error) {
			t.Fatal("managed probe used app deployment validation instead of its reviewed graph")
			return false, nil
		},
		Wake: func(context.Context, string) error { t.Fatal("probe woke target"); return nil },
		Forward: func(Target) http.Handler {
			t.Fatal("probe invoked target")
			return nil
		},
	})
	return proxy, provider
}
