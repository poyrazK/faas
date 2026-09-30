package main

import (
	"path/filepath"
	"testing"
)

func TestTCPCertificateProviderConfiguration(t *testing.T) {
	provider, err := tcpCertificateProvider("")
	if err != nil || provider != nil {
		t.Fatalf("default enabled certificate access: provider=%v err=%v", provider, err)
	}
	for _, path := range []string{"relative/certificates", filepath.Join(t.TempDir(), "missing")} {
		if provider, err := tcpCertificateProvider(path); err == nil {
			if provider != nil {
				_ = provider.Close()
			}
			t.Fatalf("invalid directory accepted: %s", path)
		}
	}
	provider, err = tcpCertificateProvider(t.TempDir())
	if err != nil || provider == nil {
		t.Fatalf("configured provider=%v err=%v", provider, err)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
}
