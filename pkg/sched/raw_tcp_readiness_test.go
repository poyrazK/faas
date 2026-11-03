package sched_test

import (
	"encoding/json"
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
		wantPath string
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
