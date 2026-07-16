package gateway

import (
	"errors"
	"strings"
	"testing"
)

func TestTLSConfigDisabledOK(t *testing.T) {
	cfg := TLSConfig{Disabled: true}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Disabled config should validate, got %v", err)
	}
}

func TestTLSConfigPartialRejects(t *testing.T) {
	cfg := TLSConfig{WildcardCertDomain: "apps.example.com"} // partial
	err := cfg.Validate()
	if !errors.Is(err, ErrTLSMisconfigured) {
		t.Errorf("partial config err = %v, want ErrTLSMisconfigured", err)
	}
}

func TestTLSConfigFullAccepts(t *testing.T) {
	cfg := TLSConfig{
		WildcardCertDomain:      "apps.example.com",
		HetznerDNSAPITokenPath:  "/etc/faas/secrets/hetzner-dns.token",
		HetznerZone:             "example.com",
		StorageDir:              "/var/lib/faas/certs",
		OnDemandHTTP01Allowlist: func(string) bool { return false },
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("full config should validate, got %v", err)
	}
}

func TestMinTLSVersionIsAtLeast13(t *testing.T) {
	// Spec §11: customers on TLS 1.0/1.1 deprecated. This test is a tripwire
	// against a careless future PR dropping the floor.
	if MinTLSVersion < 0x0304 { // tls.VersionTLS13
		t.Errorf("MinTLSVersion below TLS 1.3: 0x%04x", MinTLSVersion)
	}
	if !strings.Contains("TLS 1.3", "TLS 1.3") { // belt-and-braces: testify-style.
		t.Skip("string constants sanity")
	}
}
