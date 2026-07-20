package main

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleConfig = `
public_addr = ":443"
control_addr = "127.0.0.1:9090"
apps_domain = "apps.example.com"
apid_loopback = "http://127.0.0.1:8081"
githubd_loopback = "http://127.0.0.1:8083"

[tls]
disabled = false
wildcard_cert_domain = "apps.example.com"
hetzner_dns_api_token_path = "/etc/faas/secrets/hetzner-dns.token"
hetzner_zone = "example.com"
storage_dir = "/var/lib/faas/certs"
contact_email = "ops@example.com"
use_staging_ca = true
`

func TestLoadConfig_DefaultsWhenMissing(t *testing.T) {
	c, err := LoadConfig("/no/such/path")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if c.PublicAddr != ":8080" {
		t.Errorf("default PublicAddr = %q, want :8080", c.PublicAddr)
	}
	if c.ControlAddr != "127.0.0.1:9090" {
		t.Errorf("default ControlAddr = %q, want 127.0.0.1:9090", c.ControlAddr)
	}
	if !c.TLS.Disabled {
		t.Error("default TLS.Disabled should be true (e2e harness path)")
	}
}

func TestLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.PublicAddr != ":443" {
		t.Errorf("PublicAddr = %q, want :443", c.PublicAddr)
	}
	if c.AppsDomain != "apps.example.com" {
		t.Errorf("AppsDomain = %q, want apps.example.com", c.AppsDomain)
	}
	if c.TLS.Disabled {
		t.Error("TLS.Disabled should be false")
	}
	if c.TLS.WildcardCertDomain != "apps.example.com" {
		t.Errorf("WildcardCertDomain = %q", c.TLS.WildcardCertDomain)
	}
	if c.TLS.HetznerZone != "example.com" {
		t.Errorf("HetznerZone = %q", c.TLS.HetznerZone)
	}
	if c.TLS.StorageDir != "/var/lib/faas/certs" {
		t.Errorf("StorageDir = %q", c.TLS.StorageDir)
	}
	if c.TLS.ContactEmail != "ops@example.com" {
		t.Errorf("ContactEmail = %q, want ops@example.com", c.TLS.ContactEmail)
	}
	if !c.TLS.UseStagingCA {
		t.Error("UseStagingCA should be true from the fixture")
	}
	if got := c.resolveTLSConfig(func(string) bool { return true }); !got.UseStagingCA || got.ContactEmail != "ops@example.com" {
		t.Errorf("resolveTLSConfig lost TLS fields: %+v", got)
	}
}

// TestConfig_ResolveTLSConfig — the on-disk shape (TOMLTLSConfig) lacks the
// allowlist (function pointer). resolveTLSConfig injects it; the rest of the
// daemon consumes gateway.TLSConfig.
func TestConfig_ResolveTLSConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	called := false
	allowlist := func(string) bool { called = true; return true }
	got := c.resolveTLSConfig(allowlist)
	if got.Disabled {
		t.Error("resolved TLS.Disabled = true, want false")
	}
	if got.OnDemandHTTP01Allowlist == nil {
		t.Fatal("resolved allowlist is nil")
	}
	if !got.OnDemandHTTP01Allowlist("any.example.com") {
		t.Error("allowlist returned false on a permissive closure")
	}
	if !called {
		t.Error("allowlist was not invoked")
	}
}

func TestLoadConfig_GarbageTOMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.toml")
	if err := os.WriteFile(path, []byte("this is not = toml = =\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Error("garbage TOML should produce an error, got nil")
	}
}