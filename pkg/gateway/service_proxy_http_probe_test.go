// adr: 20261019200059 — HTTP callers probe over their canonical svc.gregale URL.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceProxyHTTPProbeOnServiceHostRequiresDeclaredBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared bool
		host     string
		probe    bool
		code     int
		stage    string
		forwards int
	}{
		{name: "declared", declared: true, host: "billing.svc.gregale:10081", probe: true, code: http.StatusNoContent, stage: "complete"},
		{name: "undeclared", host: "billing.svc.gregale:10081", probe: true, code: http.StatusForbidden, stage: "binding"},
		{name: "unmarked request is forwarded", host: "billing.svc.gregale:10081", code: http.StatusNoContent, forwards: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{
				AppID: "app-billing", Endpoints: []ServiceEndpoint{{InstanceID: "instance-a", NodeID: "node-a", Port: 8080}},
			}}
			var bindingChecks, forwards, wakes int
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider:      provider,
				ResolveCaller: func(context.Context, string) (string, error) { return "app-frontend", nil },
				AllowAlias: func(_ context.Context, caller, service string) (bool, error) {
					bindingChecks++
					return tc.declared && caller == "app-frontend" && service == "billing", nil
				},
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					return ServiceTarget{AppID: "app-billing"}, true, nil
				},
				Authorize: func(_ context.Context, caller, _ string) (ServiceCaller, error) {
					return ServiceCaller{AppID: caller}, nil
				},
				Forward: func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						forwards++
						w.WriteHeader(http.StatusNoContent)
					})
				},
				Wake: func(context.Context, string) error {
					wakes++
					return nil
				},
			})
			request := httptest.NewRequest(http.MethodHead, "http://"+tc.host+api.ServiceBindingProbePath, nil)
			request.Host = tc.host
			if tc.probe {
				request.Header.Set(api.ServiceBindingProbeRequestHeader, api.ServiceBindingProbeVersion)
			}
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, request)
			if rec.Code != tc.code || rec.Header().Get(api.ServiceBindingProbeStageHeader) != tc.stage || forwards != tc.forwards || wakes != 0 {
				t.Fatalf("code=%d stage=%q forwards=%d wakes=%d body=%q", rec.Code, rec.Header().Get(api.ServiceBindingProbeStageHeader), forwards, wakes, rec.Body.String())
			}
			if tc.probe && bindingChecks != 1 {
				t.Fatalf("probe made %d declared-binding checks, want 1", bindingChecks)
			}
		})
	}
}
