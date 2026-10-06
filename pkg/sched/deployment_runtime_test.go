// adr: 053
package sched

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentRuntimePort(t *testing.T) {
	t.Parallel()
	profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Port: 3000})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		dep  state.Deployment
		want int
	}{
		{name: "legacy default", dep: state.Deployment{}, want: 0},
		{name: "source profile", dep: state.Deployment{InferredProfile: profile}, want: 3000},
		{name: "explicit override wins", dep: state.Deployment{OverridePort: 8787, InferredProfile: profile}, want: 8787},
		{name: "function uses runner manifest port", dep: state.Deployment{Handler: "handler.handler", InferredProfile: profile}, want: api.DefaultAppPort},
		{name: "function explicit override wins", dep: state.Deployment{Handler: "handler.handler", OverridePort: 8787, InferredProfile: profile}, want: 8787},
		{name: "malformed profile", dep: state.Deployment{InferredProfile: json.RawMessage(`{"version":"v1","port":70000}`)}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := deploymentRuntimePort(tc.dep); got != tc.want {
				t.Fatalf("deploymentRuntimePort() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDeploymentScopedRuntimePortAndHealthMatchGuestManifest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime map[string]json.RawMessage
		port    int
		path    string
		grpc    bool
		service string
	}{
		{"custom HTTP", map[string]json.RawMessage{"port": json.RawMessage(`8187`), "healthz": json.RawMessage(`"/scoped-ready"`)}, 8187, "/scoped-ready", false, ""},
		{"explicit default and empty HTTP path", map[string]json.RawMessage{"port": json.RawMessage(`0`), "healthz": json.RawMessage(`""`)}, 0, "", false, ""},
		{"scoped gRPC", map[string]json.RawMessage{"port": json.RawMessage(`8187`), "healthcheck": json.RawMessage(`{"grpc":{"port":8187,"service":"reviewed"}}`)}, 8187, "", true, "reviewed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frozen := state.EnvironmentWorkloadRuntime{AppID: "app", AppType: state.AppTypeApp, Scope: "production", SourceID: "source", EnvironmentID: "environment", RevisionID: "revision", Generation: 1,
				PlanHash: strings.Repeat("a", 64), Runtime: tc.runtime}
			raw, err := json.Marshal(frozen)
			if err != nil {
				t.Fatal(err)
			}
			dep := state.Deployment{AppID: "app", Scope: "production", EnvironmentWorkloadRuntime: string(raw), OverridePort: 8787,
				OverrideHealthcheck: json.RawMessage(`{"path":"/inherited-ready"}`)}
			guest, err := state.ApplyDeploymentRuntime(api.AppManifest{Port: 8787, Healthz: "/inherited-ready"}, dep)
			if err != nil {
				t.Fatal(err)
			}
			grpc, service := healthcheckGRPCFromDep(dep)
			if deploymentRuntimePort(dep) != tc.port || deploymentRuntimePort(dep) != guest.Port || healthcheckPathFromDep(dep) != tc.path || grpc != tc.grpc || service != tc.service {
				t.Fatalf("scoped runtime differs between host and guest: guest=%+v port=%d path=%s grpc=%v service=%s", guest, deploymentRuntimePort(dep), healthcheckPathFromDep(dep), grpc, service)
			}
		})
	}
}
