package fcvm

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// A function's cold boot carries api.FunctionRunnerBootArg so guest-init
// skips characterization's L7 probes, which reached the customer's handler
// through the runner (Hunt 12 H12-6). Apps and networkless guests do not.
func TestFunctionRunnerBootArg(t *testing.T) {
	tests := []struct {
		name        string
		function    bool
		networkless bool
		want        bool
	}{
		{name: "function", function: true, want: true},
		{name: "app", function: false},
		{name: "networkless execution", function: true, networkless: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validColdSpec()
			spec.FunctionRunner, spec.Networkless = tt.function, tt.networkless
			args := strings.Fields(BuildColdBootConfig(spec, 0).BootSource.BootArgs)
			if got := slices.Contains(args, api.FunctionRunnerBootArg); got != tt.want {
				t.Fatalf("boot args %q contain %s = %v, want %v", args, api.FunctionRunnerBootArg, got, tt.want)
			}
		})
	}
}

// A raw TCP server (a managed Valkey cache) boots with
// api.RawTCPListenerBootArg so guest-init sends it no HTTP or gRPC probes
// (agent eval 2026-11-23, P8: Valkey logged them as a cross-protocol attack).
// adr: 122 — characterization L7 probes; adr: 576 — raw TCP service ports.
func TestRawTCPListenerBootArg(t *testing.T) {
	for _, rawTCP := range []bool{true, false} {
		m := &Manager{}
		spec := m.coldBootSpecForWake(netns.Config{}, WakeRequest{RoutedPortRawTCP: rawTCP}, "")
		if spec.RawTCPListener != rawTCP {
			t.Fatalf("RoutedPortRawTCP=%v: spec RawTCPListener = %v", rawTCP, spec.RawTCPListener)
		}
		cold := validColdSpec()
		cold.RawTCPListener = rawTCP
		args := strings.Fields(BuildColdBootConfig(cold, 0).BootSource.BootArgs)
		if got := slices.Contains(args, api.RawTCPListenerBootArg); got != rawTCP {
			t.Fatalf("boot args %q contain %s = %v, want %v", args, api.RawTCPListenerBootArg, got, rawTCP)
		}
	}
}

// WakeRequest.Runtime is set only for functions (the runner id).
func TestColdBootSpecForWakeMarksFunctionRunners(t *testing.T) {
	m := &Manager{}
	for runtime, want := range map[string]bool{"node22": true, "binary": true, "": false} {
		if got := m.coldBootSpecForWake(netns.Config{}, WakeRequest{Runtime: runtime}, "").FunctionRunner; got != want {
			t.Errorf("runtime %q: FunctionRunner = %v, want %v", runtime, got, want)
		}
	}
}
