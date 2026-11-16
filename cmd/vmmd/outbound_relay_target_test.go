package main

// adr: 989 — the app relay never dials outboundd in plaintext over TCP.

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutboundRelayTarget(t *testing.T) {
	cert, key, ca := writeTraceTestPKI(t)
	for _, tc := range []struct {
		name    string
		env     map[string]string
		enabled bool
		wantErr string
	}{
		{name: "unset disables the relay"},
		{name: "single-box unix socket", env: map[string]string{"FAAS_VMMD_OUTBOUND_RELAY_TARGET": "unix:///run/faas/outboundd-relay.sock"}, enabled: true},
		{name: "relative unix socket", env: map[string]string{"FAAS_VMMD_OUTBOUND_RELAY_TARGET": "unix://run/relay.sock"}, wantErr: "absolute path"},
		{name: "tcp without client TLS", env: map[string]string{"FAAS_VMMD_OUTBOUND_RELAY_TARGET": "tcp://outboundd.faas:8096"}, wantErr: "requires vmmd outbound client mTLS"},
		{name: "tcp with client TLS", env: map[string]string{
			"FAAS_VMMD_OUTBOUND_RELAY_TARGET":         "tcp://outboundd.faas:8096",
			"FAAS_VMMD_OUTBOUND_CLIENT_TLS_CERT_PATH": cert, "FAAS_VMMD_OUTBOUND_CLIENT_TLS_KEY_PATH": key, "FAAS_VMMD_OUTBOUND_CLIENT_TLS_CA_PATH": ca,
		}, enabled: true},
		{name: "tcp without port", env: map[string]string{"FAAS_VMMD_OUTBOUND_RELAY_TARGET": "tcp://outboundd.faas"}, wantErr: "tcp target"},
		{name: "unknown scheme", env: map[string]string{"FAAS_VMMD_OUTBOUND_RELAY_TARGET": "http://outboundd.faas:8096"}, wantErr: "must start with"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dial, err := outboundRelayTarget(func(name string) string { return tc.env[name] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || (dial != nil) != tc.enabled {
				t.Fatalf("dial enabled = %v, err = %v; want enabled %v", dial != nil, err, tc.enabled)
			}
		})
	}
}

// shortSocketDir keeps Unix socket paths under the 104-byte macOS limit,
// which a nested t.TempDir() can exceed.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "relay")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestOutboundRelayUnixTargetDialsTheSocket(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "relay.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{})
	go func() {
		if conn, err := ln.Accept(); err == nil {
			_ = conn.Close()
			close(accepted)
		}
	}()
	dial, err := outboundRelayTarget(func(name string) string {
		if name == "FAAS_VMMD_OUTBOUND_RELAY_TARGET" {
			return "unix://" + path
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	<-accepted
}
