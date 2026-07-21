// Tests for builderd config loading + issue #95 ResolveVMMTarget / TLS loader.
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
	if cfg.VMMDSocket != "/run/faas/vmmd.sock" {
		t.Errorf("VMMDSocket = %q, want default", cfg.VMMDSocket)
	}
	if cfg.CacheDir != "/var/cache/faas/builds" {
		t.Errorf("CacheDir = %q, want default", cfg.CacheDir)
	}
	if cfg.BuilderBase != "/srv/fc/base/builder-base.ext4" {
		t.Errorf("BuilderBase = %q, want default", cfg.BuilderBase)
	}
	// Issue #95 fields default empty.
	if cfg.VMMTarget != "" ||
		cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" {
		t.Errorf("issue #95 fields not all empty: %+v", cfg)
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

func TestConfig_LoadVMMTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadVMMTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.TLSCertPath = "/some/cert"
	if _, err := c.LoadVMMTLS(); err == nil {
		t.Errorf("partial: expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "tls_key_path") || !strings.Contains(err.Error(), "tls_ca_path") {
		t.Errorf("err = %q, want both tls_key_path and tls_ca_path named", err.Error())
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "builderd.toml")
	body := `
vmmd_target = "tcp://vmmd.internal:50051"
tls_cert_path = "/etc/faas/tls/builderd.crt"
tls_key_path = "/etc/faas/tls/builderd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
cache_dir = "/var/cache/faas/builds-test"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.VMMTarget != "tcp://vmmd.internal:50051" {
		t.Errorf("VMMTarget = %q", cfg.VMMTarget)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("TLS path overrides not all set: %+v", cfg)
	}
	if cfg.CacheDir != "/var/cache/faas/builds-test" {
		t.Errorf("CacheDir = %q", cfg.CacheDir)
	}
}
