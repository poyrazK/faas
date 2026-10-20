package main

import (
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway/egressgrpc"
)

func TestGatewayEgressPendingPath(t *testing.T) {
	private := filepath.Join(t.TempDir(), "egress-meter", "pending.json")
	explicit := filepath.Join(t.TempDir(), "explicit", "pending.json")
	for _, tc := range []struct {
		name, explicit, configured, want string
	}{
		{"production_default", "", "", egressgrpc.DefaultPendingPath},
		{"configured_private_journal", "", private, private},
		{"dependency_override", explicit, private, explicit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := gatewayEgressPendingPath(tc.explicit, func(key string) string {
				calls++
				if key != gatewayEgressPendingPathEnv {
					t.Fatalf("unexpected configuration lookup %q", key)
				}
				return tc.configured
			})
			if got != tc.want {
				t.Fatalf("journal path = %q, want %q", got, tc.want)
			}
			if tc.explicit != "" && calls != 0 {
				t.Fatal("process configuration overrode the injected journal owner")
			}
		})
	}
}
