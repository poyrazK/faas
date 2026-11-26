package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDataAPIInspectionCA(t *testing.T) {
	if got, err := loadDataAPIInspectionCA(""); err != nil || got != nil {
		t.Fatalf("disabled CA: %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "database-ca.pem")
	if err := os.WriteFile(path, ca, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := loadDataAPIInspectionCA(path); err != nil || !bytes.Equal(got, ca) {
		t.Fatalf("valid CA: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a CA"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDataAPIInspectionCA(path); err == nil {
		t.Fatal("accepted invalid CA")
	}
	if _, err := loadDataAPIInspectionCA(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("accepted missing CA")
	}
}

func TestLoadConfigDataAPIInspectionCAPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmmd.toml")
	if err := os.WriteFile(path, []byte("data_api_inspection_ca_path = \"/etc/faas/pgproxy/inspection-ca.crt\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil || config.DataAPIInspectionCAPath != "/etc/faas/pgproxy/inspection-ca.crt" {
		t.Fatalf("inspection CA configuration: %+v %v", config, err)
	}
}
