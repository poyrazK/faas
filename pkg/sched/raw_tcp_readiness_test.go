// adr: 079 — amendment 1: a raw TCP routed port also skips plan-default HTTP liveness (ADR-20261027090000).
package sched_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Container qualification 2026-10-26: a Node TCP echo built from source with
// hosting.port 5432 and a declared TCP listener on 5432 never became ready.
// The inferred profile's default /healthz sent an HTTP GET to a raw TCP
// service, and a source build had no way to opt out (ADR-20261027090000).
func TestRawTCPRoutedPortDropsTheDefaultHealthPath(t *testing.T) {
	tcp := []api.WorkloadPort{{Name: "echo", Port: 5432, Protocol: api.WorkloadPortTCP}}
	for _, tc := range []struct {
		name     string
		ports    []api.WorkloadPort
		profile  frameworkprofile.Profile
		override string
		wantPath string // informational; only the cache case is asserted
	}{
		{"a raw TCP routed port uses TCP readiness", tcp, frameworkprofile.Profile{Port: 5432, HealthPath: "/healthz"}, "", ""},
		{"an omitted protocol is TCP", []api.WorkloadPort{{Name: "echo", Port: 5432}}, frameworkprofile.Profile{Port: 5432, HealthPath: "/healthz"}, "", ""},
		{"a TCP listener on another port keeps HTTP", tcp, frameworkprofile.Profile{Port: 3000, HealthPath: "/healthz"}, "", "/healthz"},
		{"a UDP port keeps HTTP", []api.WorkloadPort{{Name: "dns", Port: 5432, Protocol: api.WorkloadPortUDP}}, frameworkprofile.Profile{Port: 5432, HealthPath: "/healthz"}, "", "/healthz"},
		{"an internal listener keeps HTTP", []api.WorkloadPort{{Port: 5432, Protocol: api.WorkloadPortTCP, Internal: true}}, frameworkprofile.Profile{Port: 5432, HealthPath: "/healthz"}, "", "/healthz"},
		{"a non-default inferred path keeps HTTP", tcp, frameworkprofile.Profile{Port: 5432, HealthPath: "/ready"}, "", "/ready"},
		{"gregale.yaml hosting.health keeps HTTP", tcp, frameworkprofile.Profile{Port: 5432, HealthPath: "/healthz", HealthFromConfig: true}, "", "/healthz"},
		{"a deploy override keeps HTTP", tcp, frameworkprofile.Profile{Port: 5432, HealthPath: "/healthz"}, `{"path":"/healthz"}`, "/healthz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.profile.Version = frameworkprofile.Version
			profile, err := json.Marshal(tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			dep := state.Deployment{ID: "dep-1", InferredProfile: profile}
			if tc.override != "" {
				dep.OverrideHealthcheck = []byte(tc.override)
			}
			app := state.App{AppProtocol: api.AppProtocolHTTP1, Manifest: state.AppManifest{Ports: tc.ports}}
			path, grpc, _ := sched.StartupProbeForApp(dep, app)
			if path != tc.wantPath || grpc {
				t.Fatalf("probe = (%q, grpc=%v), want %q", path, grpc, tc.wantPath)
			}
			// ADR-079 amendment 1: TCP readiness on the raw TCP listener
			// also tells vmmd to skip the plan-default HTTP liveness probe.
			if got, want := sched.RoutedPortRawTCPForApp(dep, app, path, grpc), tc.wantPath == ""; got != want {
				t.Fatalf("routed port raw TCP = %v, want %v", got, want)
			}
		})
	}
}

// The hosting receipt carries gregale.yaml's declaration for deployments whose
// receipt is the readiness authority.
func TestRawTCPRoutedPortHonoursTheReceiptHealthDeclaration(t *testing.T) {
	receipt, err := json.Marshal(map[string]any{
		"source":  map[string]string{"kind": "source"},
		"profile": frameworkprofile.Profile{Version: frameworkprofile.Version, Port: 5432, HealthPath: "/healthz", HealthFromConfig: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Port: 5432, HealthPath: "/healthz"})
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "echo", Port: 5432, Protocol: api.WorkloadPortTCP}}}}
	dep := state.Deployment{ID: "dep-1", APIHostingReceipt: receipt, InferredProfile: inferred}
	if path, _, _ := sched.StartupProbeForApp(dep, app); path != "/healthz" {
		t.Fatalf("declared receipt path = %q, want /healthz", path)
	}
	dep.APIHostingReceipt = nil
	if path, _, _ := sched.StartupProbeForApp(dep, app); path != "" {
		t.Fatalf("default path on a raw TCP port = %q, want TCP readiness", path)
	}
}

// adr: 20261116192126 — a managed cache's private RESP port gets TCP
// readiness and no plan-default HTTP liveness, which killed a woken cache
// about 50 s in; the same internal port on any other image keeps HTTP.
func TestManagedValkeyDeploymentUsesRawTCPProbes(t *testing.T) {
	ports := []api.WorkloadPort{{Name: "redis", Port: api.ManagedValkeyPort, Protocol: api.WorkloadPortTCP, Internal: true}}
	app := state.App{AppProtocol: api.AppProtocolHTTP1, Manifest: state.AppManifest{Port: api.ManagedValkeyPort, Ports: ports}}
	type probeCase struct {
		image    string
		wantPath string // informational; only the cache case is asserted
	}
	// Every image a cache may run: a cache that keeps an older Valkey until
	// `gregale redis update`, or a newer one apid deploys, is still a cache.
	var cases []probeCase
	for _, image := range api.ManagedValkeyImages {
		cases = append(cases, probeCase{image, ""})
	}
	cases = append(cases, probeCase{"docker.io/library/redis@sha256:" + strings.Repeat("a", 64), "/healthz"})
	for _, tc := range cases {
		dep := state.Deployment{ID: "dep-1", Kind: state.DeploymentKindImage, ImageDigest: tc.image}
		path, grpc, _ := sched.StartupProbeForApp(dep, app)
		cache := api.IsManagedValkeyImage(tc.image)
		if cache && (path != "" || grpc) {
			t.Fatalf("%s: probe = (%q, grpc=%v), want TCP accept", tc.image, path, grpc)
		}
		if got, want := sched.RoutedPortRawTCPForApp(dep, app, path, grpc), cache; got != want {
			t.Fatalf("%s: routed port raw TCP = %v, want %v", tc.image, got, want)
		}
	}
}
