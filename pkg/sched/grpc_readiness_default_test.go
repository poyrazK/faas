package sched_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
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

// Production-us rc.297: the H14-55 default turned gRPC readiness on but the
// Go framework profile's inferred /healthz stayed in the spec, and vmmd
// refused the prime boot ("healthcheck_path and healthcheck_grpc are mutually
// exclusive"). The default replaces the inferred path; HTTP apps keep it.
func TestGRPCReadinessDefaultReplacesTheInferredPath(t *testing.T) {
	profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, HealthPath: "/healthz"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, protocol, override string
		wantPath                 string
		wantGRPC                 bool
		wantService              string
	}{
		{"a gRPC app drops the inferred path", api.AppProtocolGRPC, "", "", true, ""},
		{"an explicit gRPC service drops it too", api.AppProtocolGRPC, `{"grpc":{"service":"h14.Echo"}}`, "", true, "h14.Echo"},
		{"an explicit HTTP path on a gRPC app is kept", api.AppProtocolGRPC, `{"path":"/ready"}`, "/ready", false, ""},
		{"an HTTP/1 app keeps the inferred path", api.AppProtocolHTTP1, "", "/healthz", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := state.Deployment{ID: "dep-1", InferredProfile: profile}
			if tc.override != "" {
				dep.OverrideHealthcheck = []byte(tc.override)
			}
			path, grpc, service := sched.StartupProbeForApp(dep, tc.protocol)
			if path != tc.wantPath || grpc != tc.wantGRPC || service != tc.wantService {
				t.Fatalf("probe = (%q, %v, %q), want (%q, %v, %q)", path, grpc, service, tc.wantPath, tc.wantGRPC, tc.wantService)
			}
			if path != "" && grpc {
				t.Fatal("probe carries both an HTTP path and gRPC health; vmmd refuses that spec")
			}
		})
	}
}
