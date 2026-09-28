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

func TestLoadConfig_EnvironmentOverridesSplitBoxListener(t *testing.T) {
	t.Setenv("FAAS_GITHUBD_LISTEN_ADDR", "tcp://0.0.0.0:50053")
	t.Setenv("FAAS_GITHUBD_TLS_CERT_PATH", "/etc/faas/tls/githubd/server.crt")
	t.Setenv("FAAS_GITHUBD_TLS_KEY_PATH", "/etc/faas/tls/githubd/server.key")
	t.Setenv("FAAS_GITHUBD_TLS_CA_PATH", "/etc/faas/tls/ca/ca.crt")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("missing file with deployment env: %v", err)
	}
	if cfg.ListenAddr != "tcp://0.0.0.0:50053" ||
		cfg.TLSCertPath != "/etc/faas/tls/githubd/server.crt" ||
		cfg.TLSKeyPath != "/etc/faas/tls/githubd/server.key" ||
		cfg.TLSCAPath != "/etc/faas/tls/ca/ca.crt" {
		t.Fatalf("deployment listener env not applied: %+v", cfg)
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

// TestLoadConfig_NodeNameDefaultsEmpty pins the issue #678 / ADR-093
// PR-0 surface for githubd. Empty default = single-box dev back-compat.
// PR-B reads this field at startup and constructs PGNodeVerifier when
// non-empty.
func TestLoadConfig_NodeNameDefaultsEmpty(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.NodeName != "" {
		t.Errorf("NodeName = %q, want empty (single-box default)", cfg.NodeName)
	}
}

// TestLoadConfig_NodeNameRoundTrip pins the toml round-trip for the
// githubd node_name field (also reused for the bridge dialer in PR-C1).
func TestLoadConfig_NodeNameRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "githubd.toml")
	body := `node_name = "fsn-1-githubd"` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NodeName != "fsn-1-githubd" {
		t.Errorf("NodeName = %q, want %q", cfg.NodeName, "fsn-1-githubd")
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

func TestHostKeyPath_Precedence(t *testing.T) {
	origIdentity := os.Getenv("FAAS_HOST_AGE_IDENTITY_PATH")
	origKey := os.Getenv("FAAS_HOST_AGE_KEY")
	defer func() {
		os.Setenv("FAAS_HOST_AGE_IDENTITY_PATH", origIdentity)
		os.Setenv("FAAS_HOST_AGE_KEY", origKey)
	}()

	os.Unsetenv("FAAS_HOST_AGE_IDENTITY_PATH")
	os.Unsetenv("FAAS_HOST_AGE_KEY")
	if got := hostKeyPath(); got != "/etc/faas/secrets/host.age" {
		t.Errorf("default hostKeyPath() = %q, want /etc/faas/secrets/host.age", got)
	}

	os.Setenv("FAAS_HOST_AGE_KEY", "/custom/key.age")
	if got := hostKeyPath(); got != "/custom/key.age" {
		t.Errorf("FAAS_HOST_AGE_KEY hostKeyPath() = %q, want /custom/key.age", got)
	}

	os.Setenv("FAAS_HOST_AGE_IDENTITY_PATH", "/credential/identity.age")
	if got := hostKeyPath(); got != "/credential/identity.age" {
		t.Errorf("FAAS_HOST_AGE_IDENTITY_PATH hostKeyPath() = %q, want /credential/identity.age", got)
	}
}
