// adr: 20261108124106
package wire_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// bindingPKI is a CA plus a vmmd-shaped leaf: CN is the node name and the
// shared vmmd.faas name is the SAN, as pkg/renderer issues compute leaves.
type bindingPKI struct {
	roots  *x509.CertPool
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
}

func newBindingPKI(t *testing.T) bindingPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return bindingPKI{roots: roots, caCert: ca, caKey: key}
}

func (p bindingPKI) leaf(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn}, DNSNames: []string{"vmmd.faas"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// serve answers TLS handshakes with leaf until the test ends.
func serve(t *testing.T, leaf tls.Certificate) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); _ = c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func handshake(addr string, cfg *tls.Config) error {
	d := &net.Dialer{Timeout: 5 * time.Second}
	c, err := tls.DialWithDialer(d, "tcp", addr, cfg)
	if err != nil {
		return err
	}
	return c.Close()
}

// A registered node answering on another node's target_url completes the
// shared-name handshake but not the dial bound to the node schedd meant.
func TestExpectPeerCNBindsTheDialToTheNode(t *testing.T) {
	p := newBindingPKI(t)
	addr := serve(t, p.leaf(t, "node-b.faas"))
	base := &tls.Config{RootCAs: p.roots, ServerName: "vmmd.faas", MinVersion: tls.VersionTLS13}

	if err := handshake(addr, base); err != nil {
		t.Fatalf("the unbound config should accept any CA-issued vmmd leaf: %v", err)
	}
	if err := handshake(addr, wire.ExpectPeerCN(base, "node-b.faas")); err != nil {
		t.Fatalf("the dialed node's own leaf was refused: %v", err)
	}
	for _, cn := range []string{"node-a.faas", ""} {
		err := handshake(addr, wire.ExpectPeerCN(base, cn))
		if err == nil {
			t.Fatalf("expected node %q: node-b's leaf completed the handshake", cn)
		}
		if !errors.Is(err, wire.ErrPeerNotDialedNode) {
			t.Fatalf("expected node %q: error %v does not wrap ErrPeerNotDialedNode", cn, err)
		}
	}
	if base.VerifyPeerCertificate != nil {
		t.Fatal("ExpectPeerCN changed the shared config")
	}
}

// The registry hook already on the config still runs, and runs first.
func TestExpectPeerCNKeepsTheRegistryCheck(t *testing.T) {
	p := newBindingPKI(t)
	addr := serve(t, p.leaf(t, "node-b.faas"))
	refused := errors.New("registry refused")
	base := &tls.Config{
		RootCAs: p.roots, ServerName: "vmmd.faas", MinVersion: tls.VersionTLS13,
		VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error { return refused },
	}
	if err := handshake(addr, wire.ExpectPeerCN(base, "node-b.faas")); !errors.Is(err, refused) {
		t.Fatalf("got %v, want the registry hook's error", err)
	}
	if wire.ExpectPeerCN(nil, "node-b.faas") != nil {
		t.Fatal("a nil (unix socket) config gained TLS")
	}
}
