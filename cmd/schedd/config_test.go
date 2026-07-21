// Tests for schedd config loading: defaults, missing file, partial TOML,
// plus the issue #95 ResolveListenTarget / ResolveVMMTarget / TLS loaders.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_MissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.SocketPath != "/run/faas/schedd.sock" {
		t.Errorf("SocketPath = %q, want default", cfg.SocketPath)
	}
	if cfg.VMMDSocket != "/run/faas/vmmd.sock" {
		t.Errorf("VMMDSocket = %q, want default", cfg.VMMDSocket)
	}
	if cfg.GatewaySynthSocket != "/run/faas/gatewayd-internal.sock" {
		t.Errorf("GatewaySynthSocket = %q, want default", cfg.GatewaySynthSocket)
	}
	if cfg.OwnerUser != "faas-schedd" {
		t.Errorf("OwnerUser = %q, want default", cfg.OwnerUser)
	}
	// Issue #95: TLS/target fields all default empty.
	if cfg.ListenAddr != "" || cfg.VMMTarget != "" ||
		cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" ||
		cfg.VMMTLSCertPath != "" || cfg.VMMTLSKeyPath != "" || cfg.VMMTLSCAPath != "" {
		t.Errorf("issue #95 fields not all empty: %+v", cfg)
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedd.toml")
	body := `
listen_addr = "tcp://0.0.0.0:50051"
vmmd_target = "tcp://vmmd.internal:50051"
tls_cert_path = "/etc/faas/tls/schedd.crt"
tls_key_path = "/etc/faas/tls/schedd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
vmmd_tls_cert_path = "/etc/faas/tls/vmmd-client.crt"
vmmd_tls_key_path = "/etc/faas/tls/vmmd-client.key"
vmmd_tls_ca_path = "/etc/faas/tls/ca.pem"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ListenAddr != "tcp://0.0.0.0:50051" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.VMMTarget != "tcp://vmmd.internal:50051" {
		t.Errorf("VMMTarget = %q", cfg.VMMTarget)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("server TLS path overrides not all set: %+v", cfg)
	}
	if cfg.VMMTLSCertPath == "" || cfg.VMMTLSKeyPath == "" || cfg.VMMTLSCAPath == "" {
		t.Errorf("vmmd TLS path overrides not all set: %+v", cfg)
	}
}

func TestConfig_ResolveListenTarget(t *testing.T) {
	c := &Config{SocketPath: "/run/faas/schedd.sock"}
	if got := c.ResolveListenTarget(); got != "unix:///run/faas/schedd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/schedd.sock", got)
	}
	c.ListenAddr = "tcp://0.0.0.0:50051"
	if got := c.ResolveListenTarget(); got != "tcp://0.0.0.0:50051" {
		t.Errorf("explicit = %q, want tcp://0.0.0.0:50051", got)
	}
}

func TestConfig_ResolveVMMTarget(t *testing.T) {
	c := &Config{VMMDSocket: "/run/faas/vmmd.sock"}
	if got := c.ResolveVMMTarget(); got != "unix:///run/faas/vmmd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/vmmd.sock", got)
	}
	c.VMMTarget = "tcp://vmmd.internal:50051"
	if got := c.ResolveVMMTarget(); got != "tcp://vmmd.internal:50051" {
		t.Errorf("explicit = %q, want tcp://vmmd.internal:50051", got)
	}
}

func TestConfig_LoadServerTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadServerTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.TLSCertPath = "/some/cert"
	if _, err := c.LoadServerTLS(); err == nil {
		t.Errorf("partial: expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "tls_key_path") || !strings.Contains(err.Error(), "tls_ca_path") {
		t.Errorf("err = %q, want both tls_key_path and tls_ca_path named", err.Error())
	}
}

func TestConfig_LoadVMMTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadVMMTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.VMMTLSCertPath = "/some/cert"
	if _, err := c.LoadVMMTLS(); err == nil {
		t.Errorf("partial: expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "vmmd_tls_key_path") || !strings.Contains(err.Error(), "vmmd_tls_ca_path") {
		t.Errorf("err = %q, want both vmmd_tls_key_path and vmmd_tls_ca_path named", err.Error())
	}
}
