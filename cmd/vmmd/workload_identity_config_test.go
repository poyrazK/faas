package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadWorkloadIdentitySigner(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.key")
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_WORKLOAD_IDENTITY_KEY_PATH", path)
	t.Setenv("FAAS_WORKLOAD_IDENTITY_ISSUER", "https://identity.example.test")
	t.Setenv("FAAS_WORKLOAD_IDENTITY_TTL_SECONDS", "90")
	signer, err := loadWorkloadIdentitySigner()
	if err != nil || signer == nil {
		t.Fatalf("load signer = %v, %v", signer, err)
	}
	tok, err := signer.Mint(nowForTest(), "acct", "app", "inst", "aud")
	if err != nil || tok.ExpiresIn != 90 {
		t.Fatalf("mint = %+v, %v", tok, err)
	}
}

func TestLoadWorkloadIdentitySignerDisabled(t *testing.T) {
	t.Setenv("FAAS_WORKLOAD_IDENTITY_KEY_PATH", "")
	signer, err := loadWorkloadIdentitySigner()
	if err != nil || signer != nil {
		t.Fatalf("disabled signer = %v, %v", signer, err)
	}
}

func nowForTest() time.Time { return time.Unix(1_700_000_000, 0) }
