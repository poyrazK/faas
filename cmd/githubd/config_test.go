// Tests for githubd config loading: defaults, missing file, parse errors,
// plus the issue #95 ResolveListenTarget / LoadServerTLS helpers.
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
	if cfg.HTTPAddr != "127.0.0.1:8083" {
		t.Errorf("HTTPAddr = %q, want default", cfg.HTTPAddr)
	}
	if cfg.SocketPath != "/run/faas/githubd.sock" {
		t.Errorf("SocketPath = %q, want default", cfg.SocketPath)
	}
	// Issue #95: TLS/listen defaults all empty.
	if cfg.ListenAddr != "" ||
		cfg.TLSCertPath != "" || cfg.TLSKeyPath != "" || cfg.TLSCAPath != "" {
		t.Errorf("issue #95 fields not all empty: %+v", cfg)
	}
}

func TestLoadConfig_OverridesFromTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "githubd.toml")
	body := `
http_addr = "127.0.0.1:9083"
socket_path = "/run/faas/other-gh.sock"
listen_addr = "tcp://0.0.0.0:50053"
tls_cert_path = "/etc/faas/tls/githubd.crt"
tls_key_path = "/etc/faas/tls/githubd.key"
tls_ca_path = "/etc/faas/tls/ca.pem"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9083" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.SocketPath != "/run/faas/other-gh.sock" {
		t.Errorf("SocketPath = %q", cfg.SocketPath)
	}
	if cfg.ListenAddr != "tcp://0.0.0.0:50053" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" || cfg.TLSCAPath == "" {
		t.Errorf("TLS path overrides not all set: %+v", cfg)
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

func TestConfig_ResolveListenTarget(t *testing.T) {
	c := &Config{SocketPath: "/run/faas/githubd.sock"}
	if got := c.ResolveListenTarget(); got != "unix:///run/faas/githubd.sock" {
		t.Errorf("fallback = %q, want unix:///run/faas/githubd.sock", got)
	}
	c.ListenAddr = "tcp://0.0.0.0:50053"
	if got := c.ResolveListenTarget(); got != "tcp://0.0.0.0:50053" {
		t.Errorf("explicit = %q, want tcp://0.0.0.0:50053", got)
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
