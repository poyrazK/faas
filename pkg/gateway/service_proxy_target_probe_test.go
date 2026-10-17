package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceProxyTargetProbeDoesNotQualifyOtherReplicas(t *testing.T) {
	target := uuid.NewString()
	for _, scenario := range []struct {
		name      string
		endpoints []ServiceEndpoint
		live      bool
		target    string
		status    int
	}{
		{"old replica", []ServiceEndpoint{{DeploymentID: uuid.NewString()}}, true, target, 503},
		{"exact replica", []ServiceEndpoint{{DeploymentID: target}}, true, target, 204},
		{"not live", []ServiceEndpoint{{DeploymentID: target}}, false, target, 422},
		{"malformed", []ServiceEndpoint{{DeploymentID: target}}, true, "invalid", 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for i := range scenario.endpoints {
				scenario.endpoints[i].InstanceID = "instance"
				scenario.endpoints[i].NodeID = "node"
				scenario.endpoints[i].Port = 8080
			}
			provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "billing", Endpoints: scenario.endpoints}}
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider: provider, ResolveCaller: func(context.Context, string) (string, error) { return "caller", nil },
				AllowAlias: func(context.Context, string, string) (bool, error) { return true, nil },
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "billing"}, true, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) { return ServiceCaller{}, nil },
				ValidateDeployment: func(_ context.Context, app, id string) (bool, error) {
					if app != "billing" || id != target {
						t.Fatal("wrong target validation")
					}
					return scenario.live, nil
				},
				Wake:    func(context.Context, string) error { t.Fatal("probe woke target"); return nil },
				Forward: func(Target) http.Handler { t.Fatal("probe invoked target"); return nil },
			})
			req := httptest.NewRequest(http.MethodHead, "https://billing.internal"+api.ServiceBindingProbePath, nil)
			req.Header.Set(api.ServiceBindingProbeRequestHeader, api.ServiceBindingProbeVersion)
			req.Header.Set(api.TargetDeploymentHeader, scenario.target)
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != scenario.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if scenario.status == 204 && rec.Header().Get(api.TargetDeploymentHeader) != target {
				t.Fatal("missing exact target echo")
			}
		})
	}
}
