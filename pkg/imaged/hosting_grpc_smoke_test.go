package imaged

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestRouteConnectivitySmokeForGRPCApps — on production-us every
// app_protocol=grpc deploy passed its in-VM gRPC readiness and then failed
// "post-readiness smoke failed: health probe returned HTTP 415": the public
// smoke sent GET /healthz to a gRPC server. gRPC apps, and any deployment
// whose readiness is the gRPC health check, now prove route connectivity.
func TestRouteConnectivitySmokeForGRPCApps(t *testing.T) {
	grpcOverride, _ := json.Marshal(map[string]any{"grpc": map[string]string{"service": ""}})
	httpOverride, _ := json.Marshal(map[string]any{"path": "/ready"})
	for _, tc := range []struct {
		name string
		app  state.App
		dep  state.Deployment
		want bool
	}{
		{"http app", state.App{Slug: "web", AppProtocol: "http1"}, state.Deployment{Kind: state.DeploymentKindTarball}, false},
		{"grpc protocol", state.App{Slug: "rpc", AppProtocol: "grpc"}, state.Deployment{Kind: state.DeploymentKindTarball}, true},
		{"grpc readiness override", state.App{Slug: "rpc2", AppProtocol: "http2"}, state.Deployment{Kind: state.DeploymentKindTarball, OverrideHealthcheck: grpcOverride}, true},
		{"http readiness override", state.App{Slug: "web2", AppProtocol: "http1"}, state.Deployment{Kind: state.DeploymentKindTarball, OverrideHealthcheck: httpOverride}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := routeConnectivitySmoke(tc.app, tc.dep); got != tc.want {
				t.Fatalf("routeConnectivitySmoke = %v, want %v", got, tc.want)
			}
		})
	}
}
