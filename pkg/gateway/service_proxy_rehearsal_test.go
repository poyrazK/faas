// adr: 20261017201005
package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestRehearsalNetworkServiceProxyRefusesDevelopmentTunnel(t *testing.T) {
	for _, contained := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "contained"}[contained], func(t *testing.T) {
			forwarded := false
			proxy := gateway.NewServiceProxy(gateway.ServiceProxyConfig{
				Resolve: func(context.Context, string, string) (gateway.ServiceTarget, bool, error) {
					return gateway.ServiceTarget{AppID: "target"}, true, nil
				},
				ResolveCallerIdentity: func(context.Context, string) (string, string, error) { return "caller", "deployment", nil },
				Authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
					caller := gateway.ServiceCaller{AppID: "caller", AccountID: "account"}
					if contained {
						caller.RehearsalRunID = strings.Repeat("a", 32)
					}
					return caller, nil
				},
				DevBridge: func(w http.ResponseWriter, _ *http.Request, _ gateway.ServiceCaller, _ gateway.ServiceTarget, _ string) {
					forwarded = true
					w.WriteHeader(http.StatusAccepted)
				},
			})
			req := httptest.NewRequest("GET", "/v1/internal/services/worker/path", nil)
			req.Header.Set(gateway.ServiceProxyCallerAppHeader, "caller")
			req.Header.Set(devbridge.SessionHeader, "synthetic-session")
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, req)
			if contained && (forwarded || response.Code != http.StatusForbidden) {
				t.Fatalf("contained caller reached tunnel: %d %s", response.Code, response.Body.String())
			}
			if !contained && (!forwarded || response.Code != http.StatusAccepted) {
				t.Fatalf("ordinary tunnel changed: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
