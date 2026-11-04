// adr: 20261103163954
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/edgedns"
)

// The manager reads its account key and recipient from the credential
// files and orders nothing until it runs.
func TestNewCertManagerReadsItsCredentials(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalECPrivateKey(key)
	keyFile := filepath.Join(dir, "acme_account_key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o400); err != nil {
		t.Fatal(err)
	}
	id, _ := age.GenerateX25519Identity()
	recipientFile := filepath.Join(dir, "edge_tls_recipient")
	if err := os.WriteFile(recipientFile, []byte(id.Recipient().String()+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	dns, _ := edgedns.NewCloudflare("http://127.0.0.1:0", "tok")
	c := certConfig{names: []string{"*.gregale.dev"}, zoneID: "zone-1", ca: edgecert.CALetsEncryptStaging, accountKeyFile: keyFile, recipientFile: recipientFile}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := newCertManager(c, dns, nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	if m.Recipient.String() != id.Recipient().String() || m.Orderer.CA() != edgecert.CALetsEncryptStaging {
		t.Fatalf("manager = %+v", m)
	}
	c.accountKeyFile = recipientFile
	if _, err := newCertManager(c, dns, nil, nil, log); err == nil || !strings.Contains(err.Error(), "ACME account key") {
		t.Fatalf("a recipient as the account key: err = %v", err)
	}
}
