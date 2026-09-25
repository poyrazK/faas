package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServiceProxyCA(t *testing.T) {
	if data, err := loadServiceProxyCA(""); err != nil || data != nil {
		t.Fatalf("disabled CA = %q, %v", data, err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	makeCert := func(isCA bool) []byte {
		t.Helper()
		template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: isCA, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	path := filepath.Join(t.TempDir(), "service-ca.crt")
	for _, tc := range []struct {
		name    string
		content []byte
		ok      bool
	}{
		{"valid CA", makeCert(true), true},
		{"leaf", makeCert(false), false},
		{"private key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("secret")}), false},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}
			data, err := loadServiceProxyCA(path)
			if (err == nil) != tc.ok {
				t.Fatalf("loadServiceProxyCA = %q, %v; want ok=%v", data, err, tc.ok)
			}
		})
	}
}
