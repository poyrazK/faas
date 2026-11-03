package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// An empty or zero cap meant unlimited raw TCP connections, so a few thousand
// idle connections could push gatewayd-public past its memory cap (bug hunt
// 2026-10-27 H23). Both now mean the platform default.
func TestTCPConnectionCapDefaults(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{
		{"", api.RawTCPMaxConnectionsDefault},
		{"0", api.RawTCPMaxConnectionsDefault},
		{"200", 200},
	} {
		t.Setenv("FAAS_TCPD_MAX_CONNECTIONS", tc.env)
		got, err := tcpConnectionCap("FAAS_TCPD_MAX_CONNECTIONS", api.RawTCPMaxConnectionsDefault)
		if err != nil || got != tc.want {
			t.Fatalf("env %q: %d, %v; want %d", tc.env, got, err, tc.want)
		}
	}
	t.Setenv("FAAS_TCPD_MAX_CONNECTIONS", "-1")
	if _, err := tcpConnectionCap("FAAS_TCPD_MAX_CONNECTIONS", api.RawTCPMaxConnectionsDefault); err == nil {
		t.Fatal("a negative cap was accepted")
	}
}
