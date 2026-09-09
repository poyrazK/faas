package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOperatorScheddConfigMissingUsesUnixDefault(t *testing.T) {
	cfg, err := loadOperatorScheddConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("loadOperatorScheddConfig: %v", err)
	}
	if got, want := cfg.Target, "unix:///run/faas/schedd.sock"; got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
	if cfg.CertPath != "" || cfg.KeyPath != "" || cfg.CAPath != "" {
		t.Fatalf("TLS paths = %#v, want empty single-box defaults", cfg)
	}
}

func TestLoadOperatorScheddConfigSplitBox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meterd.toml")
	body := strings.Join([]string{
		`schedd_socket = "tcp://schedd.faas:9091"`,
		`schedd_tls_cert_path = "/etc/faas/tls/meterd/schedd-client.crt"`,
		`schedd_tls_key_path = "/etc/faas/tls/meterd/schedd-client.key"`,
		`schedd_tls_ca_path = "/etc/faas/tls/ca/ca.crt"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadOperatorScheddConfig(path)
	if err != nil {
		t.Fatalf("loadOperatorScheddConfig: %v", err)
	}
	if got, want := cfg.Target, "tcp://schedd.faas:9091"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	if got, want := cfg.CertPath, "/etc/faas/tls/meterd/schedd-client.crt"; got != want {
		t.Errorf("cert path = %q, want %q", got, want)
	}
	if got, want := cfg.KeyPath, "/etc/faas/tls/meterd/schedd-client.key"; got != want {
		t.Errorf("key path = %q, want %q", got, want)
	}
	if got, want := cfg.CAPath, "/etc/faas/tls/ca/ca.crt"; got != want {
		t.Errorf("CA path = %q, want %q", got, want)
	}
}

func TestLoadOperatorScheddConfigRejectsPartialTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meterd.toml")
	if err := os.WriteFile(path, []byte(`schedd_tls_cert_path = "/cert.pem"`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := resolveOperatorScheddConnection(path)
	if err == nil || !strings.Contains(err.Error(), "schedd_tls_key_path") || !strings.Contains(err.Error(), "schedd_tls_ca_path") {
		t.Fatalf("partial TLS error = %v, want missing key and CA fields", err)
	}
}

func TestResolveOperatorScheddConnectionEnvTargetWins(t *testing.T) {
	t.Setenv("FAAS_SCHEDD_ADDR", "unix:///tmp/test-schedd.sock")
	target, tlsCfg, err := resolveOperatorScheddConnection(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("resolveOperatorScheddConnection: %v", err)
	}
	if got, want := target, "unix:///tmp/test-schedd.sock"; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	if tlsCfg != nil {
		t.Errorf("TLS config = %#v, want nil for missing single-box config", tlsCfg)
	}
}
