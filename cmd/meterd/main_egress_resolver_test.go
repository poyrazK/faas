package main

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway/egresssocket"
)

// TestEgressSocketPathResolver pins the meterd-side resolution
// chain that the daemon constructs at startup: cfg.EgressSocket is
// the new precedence-winning field, the legacy GatewayEgressSocket
// is the read-both-prefer-new fallback, the env vars are read by
// ResolveFromOS via the production os.LookupEnv wrapper, and the
// resolver is contractually never-empty. Mirrors the egress_grpc
// test in cmd/gatewayd-internal: same precedence, just driven
// through the meterd config struct.
func TestEgressSocketPathResolver(t *testing.T) {
	cases := []struct {
		name              string
		newCfg, legacyCfg string
		envNew, envLegacy string
		want              string
	}{
		{
			name:      "env new beats legacy env",
			envNew:    "/tmp/new.sock",
			envLegacy: "/tmp/legacy-env.sock",
			want:      "/tmp/new.sock",
		},
		{
			name:      "env legacy is fallback when env new is unset",
			envLegacy: "/tmp/legacy-env.sock",
			want:      "/tmp/legacy-env.sock",
		},
		{
			name:   "cfg new beats legacy cfg when env vars are empty",
			newCfg: "/tmp/new-cfg.sock",
			want:   "/tmp/new-cfg.sock",
		},
		{
			name:      "cfg legacy is fallback when only it is populated",
			legacyCfg: "/tmp/legacy-cfg.sock",
			want:      "/tmp/legacy-cfg.sock",
		},
		{
			name: "const default kicks in when every source is empty",
			want: "/run/faas/egress.sock",
		},
		{
			name:   "env beats cfg regardless of cfg content",
			envNew: "/tmp/env-wins.sock",
			newCfg: "/tmp/cfg-loses.sock",
			want:   "/tmp/env-wins.sock",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv hermeticity per case. Cleanup undefs the
			// vars so the next case starts from empty.
			t.Setenv("FAAS_EGRESS_SOCKET", tc.envNew)
			t.Setenv("FAAS_GATEWAY_EGRESS_SOCKET", tc.envLegacy)
			t.Cleanup(func() {
				os.Unsetenv("FAAS_EGRESS_SOCKET")
				os.Unsetenv("FAAS_GATEWAY_EGRESS_SOCKET")
			})
			getEnv := func(key string) string {
				if v, ok := os.LookupEnv(key); ok {
					return v
				}
				return ""
			}
			got := egresssocket.ResolveFromOS(getEnv, tc.newCfg, tc.legacyCfg)
			if got != tc.want {
				t.Errorf("ResolveFromOS = %q, want %q", got, tc.want)
			}
			if got == "" {
				t.Errorf("ResolveFromOS returned empty string (resolver contract: never-empty)")
			}
		})
	}
}
