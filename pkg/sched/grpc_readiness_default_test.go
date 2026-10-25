package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us rc.294: h14-grpc deployed with --app-protocol grpc and no
// --healthcheck-grpc failed app_startup_timeout after a 575 s build, because
// readiness probed HTTP /healthz on a gRPC server (Hunt 14 H14-55).
func TestGRPCAppsDefaultToGRPCReadiness(t *testing.T) {
	for _, tc := range []struct {
		name        string
		protocol    string
		override    string
		wantGRPC    bool
		wantService string
	}{
		{"a gRPC app without a probe uses gRPC health", api.AppProtocolGRPC, "", true, ""},
		{"an explicit gRPC service still wins", api.AppProtocolGRPC, `{"grpc":{"service":"h14.Echo"}}`, true, "h14.Echo"},
		{"an explicit HTTP path keeps HTTP readiness", api.AppProtocolGRPC, `{"path":"/ready"}`, false, ""},
		{"an HTTP/2 app keeps HTTP readiness", api.AppProtocolHTTP2, "", false, ""},
		{"an HTTP/1 app keeps HTTP readiness", api.AppProtocolHTTP1, "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := state.Deployment{ID: "dep-1"}
			if tc.override != "" {
				dep.OverrideHealthcheck = []byte(tc.override)
			}
			grpc, service := sched.HealthcheckGRPCForApp(dep, tc.protocol)
			if grpc != tc.wantGRPC || service != tc.wantService {
				t.Fatalf("readiness = (%v, %q), want (%v, %q)", grpc, service, tc.wantGRPC, tc.wantService)
			}
		})
	}
}
