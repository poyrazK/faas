package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	overrideStableID    = "11111111-1111-4111-8111-111111111111"
	overrideCandidateID = "22222222-2222-4222-8222-222222222222"
)

func overrideServiceRequest(proxy *ServiceProxy, values ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://gateway/v1/internal/services/orders/health", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "app-client")
	req.Header.Set(api.VersionKeyHeader, "customer-42")
	if values != nil {
		req.Header[api.TargetDeploymentHeader] = values
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	return rec
}

func TestServiceProxyExactDeploymentOverridesAffinityAndDoesNotPropagate(t *testing.T) {
	provider := &affinityServiceProvider{
		selectedDeployment: overrideStableID,
		snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
			{InstanceID: "stable-1", NodeID: "node-a", DeploymentID: overrideStableID, Port: 8080},
			{InstanceID: "candidate-1", NodeID: "node-b", DeploymentID: overrideCandidateID, Port: 8080},
		}},
	}
	var gotTarget Target
	var gotKey, gotOverride string
	var authorized, validated bool
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			authorized = true
			return ServiceCaller{}, nil
		},
		ValidateDeployment: func(_ context.Context, appID, deploymentID string) (bool, error) {
			if !authorized {
				t.Fatal("deployment validation ran before binding authorization")
			}
			validated = true
			return appID == "app-orders" && deploymentID == overrideCandidateID, nil
		},
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotTarget = target
				gotKey = r.Header.Get(api.VersionKeyHeader)
				gotOverride = r.Header.Get(api.TargetDeploymentHeader)
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	rec := overrideServiceRequest(proxy, overrideCandidateID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if !validated || gotTarget.DeploymentID != overrideCandidateID {
		t.Fatalf("validated = %v, target = %+v; want candidate", validated, gotTarget)
	}
	if gotKey != "customer-42" || gotOverride != "" {
		t.Fatalf("forwarded key/override = %q/%q, want key retained and override stripped", gotKey, gotOverride)
	}
}

func TestServiceProxyExactDeploymentWakesColdZeroPercentTarget(t *testing.T) {
	woke := false
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
		{InstanceID: "stable-1", NodeID: "node-a", DeploymentID: overrideStableID, Port: 8080},
	}}}
	var gotTarget Target
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, nil
		},
		ValidateDeployment: func(_ context.Context, _, deploymentID string) (bool, error) {
			return deploymentID == overrideCandidateID, nil // live at 0%, initially cold
		},
		WakeDeployment: func(_ context.Context, appID, deploymentID string) error {
			if appID != "app-orders" || deploymentID != overrideCandidateID {
				t.Fatalf("wake = %q/%q, want exact candidate", appID, deploymentID)
			}
			woke = true
			provider.snapshot.Endpoints = append(provider.snapshot.Endpoints, ServiceEndpoint{
				InstanceID: "candidate-1", NodeID: "node-b", DeploymentID: overrideCandidateID, Port: 8080,
			})
			return nil
		},
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				gotTarget = target
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	rec := overrideServiceRequest(proxy, overrideCandidateID)
	if rec.Code != http.StatusNoContent || !woke || gotTarget.DeploymentID != overrideCandidateID {
		t.Fatalf("status/woke/target = %d/%v/%+v, want candidate 204", rec.Code, woke, gotTarget)
	}
}

func TestServiceProxyExactDeploymentFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		values     []string
		validator  ServiceProxyDeploymentValidator
		wantStatus int
	}{
		{"empty", []string{""}, nil, http.StatusBadRequest},
		{"malformed", []string{"not-a-deployment"}, nil, http.StatusBadRequest},
		{"duplicate", []string{overrideCandidateID, overrideStableID}, nil, http.StatusBadRequest},
		{"joined", []string{overrideCandidateID + "," + overrideStableID}, nil, http.StatusBadRequest},
		{"not live", []string{overrideCandidateID}, func(context.Context, string, string) (bool, error) { return false, nil }, http.StatusUnprocessableEntity},
		{"validator missing", []string{overrideCandidateID}, nil, http.StatusServiceUnavailable},
		{"store unavailable", []string{overrideCandidateID}, func(context.Context, string, string) (bool, error) { return false, errors.New("db down") }, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forwarded := false
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-orders", Endpoints: []ServiceEndpoint{
					{InstanceID: "stable-1", NodeID: "node-a", DeploymentID: overrideStableID, Port: 8080},
				}}},
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "app-orders"}, true, nil
				},
				Authorize:          func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
				ValidateDeployment: tc.validator,
				Forward: func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						forwarded = true
						w.WriteHeader(http.StatusNoContent)
					})
				},
			})
			rec := overrideServiceRequest(proxy, tc.values...)
			if rec.Code != tc.wantStatus || forwarded {
				t.Fatalf("status/forwarded = %d/%v, want %d/false: %s", rec.Code, forwarded, tc.wantStatus, rec.Body.String())
			}
			if tc.name == "store unavailable" && strings.Contains(rec.Body.String(), "db down") {
				t.Fatal("store error leaked to caller")
			}
		})
	}
}

func TestServiceProxyExactDeploymentAuthorizationPrecedesValidation(t *testing.T) {
	validated := false
	proxy := NewServiceProxy(ServiceProxyConfig{
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: "app-orders"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{}, ErrServiceProxyDenied
		},
		ValidateDeployment: func(context.Context, string, string) (bool, error) {
			validated = true
			return true, nil
		},
	})
	rec := overrideServiceRequest(proxy, overrideCandidateID)
	if rec.Code != http.StatusForbidden || validated {
		t.Fatalf("status/validated = %d/%v, want 403/false", rec.Code, validated)
	}
}
