// Tests for vmmd config loading: defaults, missing file, parse errors.
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
	if cfg.SocketPath != "/run/faas/vmmd.sock" {
		t.Errorf("SocketPath = %q, want default", cfg.SocketPath)
	}
	if cfg.KernelPath != "/srv/fc/base/vmlinux-6.1" {
		t.Errorf("KernelPath = %q, want default", cfg.KernelPath)
	}
	if cfg.OwnerUser != "faas-vmmd" {
		t.Errorf("OwnerUser = %q, want default", cfg.OwnerUser)
	}
	if cfg.MetricsAddr != "" {
		t.Errorf("MetricsAddr = %q, want empty (disabled)", cfg.MetricsAddr)
	}
	// Issue #95: server-mTLS paths default empty.
	if cfg.ListenAddr != "" || cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" {
		t.Errorf("TLS/listen defaults not all empty: %+v", cfg)
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	body := `
socket_path = "/run/faas/other.sock"
listen_addr = "tcp://0.0.0.0:50051"
tls_cert_path = "/etc/faas/tls/vmmd.crt"
tls_key_path = "/etc/faas/tls/vmmd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
metrics_addr = "127.0.0.1:9090"
owner_user = "vmmd-other"
kernel_path = "/srv/fc/alt/vmlinux"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SocketPath != "/run/faas/other.sock" {
		t.Errorf("SocketPath = %q", cfg.SocketPath)
	}
	if cfg.ListenAddr != "tcp://0.0.0.0:50051" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("TLS path overrides not all set: %+v", cfg)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
	}
	if cfg.OwnerUser != "vmmd-other" {
		t.Errorf("OwnerUser = %q", cfg.OwnerUser)
	}
	if cfg.KernelPath != "/srv/fc/alt/vmlinux" {
		t.Errorf("KernelPath = %q", cfg.KernelPath)
	}
}

func TestLoadConfig_PartialTOMLKeepsDefaults(t *testing.T) {
	// Only override one field; the rest must stay at the defaults.
	path := filepath.Join(t.TempDir(), "partial.toml")
	body := `metrics_addr = "127.0.0.1:9090"` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
	}
	if cfg.SocketPath != "/run/faas/vmmd.sock" {
		t.Errorf("SocketPath = %q (default lost after partial unmarshal)", cfg.SocketPath)
	}
}

func TestLoadConfig_BadTOMLErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(path, []byte("not valid toml === ==="), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should mention parse failure", err.Error())
	}
}

func TestLoadConfig_ReadErrorOther(t *testing.T) {
	// A path that exists but is a directory produces a non-ENOENT read error.
	dir := t.TempDir()
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error reading a directory")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should not be 'not found' — directory read is a real error", err.Error())
	}
}

// Issue #95: ResolveListenTarget prefers listen_addr, falls back to
// unix://+socket_path. The fallback must remain unchanged for
// single-box deployments.
func TestConfig_ResolveListenTarget(t *testing.T) {
	c := &Config{SocketPath: "/run/faas/vmmd.sock"}
	if got := c.ResolveListenTarget(); got != "unix:///run/faas/vmmd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/vmmd.sock", got)
	}
	c.ListenAddr = "tcp://0.0.0.0:50051"
	if got := c.ResolveListenTarget(); got != "tcp://0.0.0.0:50051" {
		t.Errorf("explicit = %q, want tcp://0.0.0.0:50051", got)
	}
}

// Issue #95: LoadServerTLS rejects partial cluster — the wire helper
// names the missing fields. Empty config returns (nil, nil) and is the
// single-box path.
func TestConfig_LoadServerTLS(t *testing.T) {
	c := &Config{}
	tls, err := c.LoadServerTLS()
	if err != nil || tls != nil {
		t.Errorf("all-empty: tls=%v err=%v, want nil", tls, err)
	}

	c.TLSCertPath = "/some/cert"
	if _, err := c.LoadServerTLS(); err == nil {
		t.Errorf("partial (cert only): expected error naming missing fields")
	} else if !strings.Contains(err.Error(), "tls_key_path") || !strings.Contains(err.Error(), "tls_ca_path") {
		t.Errorf("err = %q, want both tls_key_path and tls_ca_path named", err.Error())
	}
}
