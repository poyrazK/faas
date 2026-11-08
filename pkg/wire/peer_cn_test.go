package wire

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

func leafFor(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// A dial to one node accepts only that node's leaf, after the base checks
// (R8-12: the node verifier alone accepts any registered node's CN).
func TestTLSForPeerCN(t *testing.T) {
	if TLSForPeerCN(nil, "node-a") != nil {
		t.Fatal("a nil base must stay nil (plaintext unix sockets)")
	}
	base := &tls.Config{MinVersion: tls.VersionTLS13}
	if TLSForPeerCN(base, "") != base {
		t.Fatal("an unknown name must leave the base unchanged")
	}
	cfg := TLSForPeerCN(base, "node-a")
	if base.VerifyPeerCertificate != nil {
		t.Fatal("the base config was mutated")
	}
	if err := cfg.VerifyPeerCertificate([][]byte{leafFor(t, "node-a")}, nil); err != nil {
		t.Fatalf("the dialled node's own leaf was refused: %v", err)
	}
	if err := cfg.VerifyPeerCertificate([][]byte{leafFor(t, "node-b")}, nil); err == nil {
		t.Fatal("another registered node's leaf was accepted")
	}
	if err := cfg.VerifyPeerCertificate(nil, nil); err == nil {
		t.Fatal("a peer with no certificate was accepted")
	}
	refused := errors.New("unregistered")
	base.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error { return refused }
	if err := TLSForPeerCN(base, "node-a").VerifyPeerCertificate([][]byte{leafFor(t, "node-a")}, nil); !errors.Is(err, refused) {
		t.Fatalf("the base verifier must still run first, got %v", err)
	}
}
