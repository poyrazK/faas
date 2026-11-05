// adr: 1027 — the import relay reaches only public addresses, insists on
// TLS from the source, and verifies the source's certificate.
package dbvm

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestImportSourcesMustBePublic(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.9", "192.168.1.1", "169.254.169.254", "100.64.7.1", "198.19.0.5",
		"0.0.0.0", "::1", "fc00::1", "fe80::1", "::ffff:10.0.0.1", "224.0.0.1"} {
		if publicAddr(netip.MustParseAddr(addr)) {
			t.Errorf("%s counted as public", addr)
		}
	}
	for _, addr := range []string{"3.5.140.2", "34.226.10.1", "2600:1f18::1", "::ffff:3.5.140.2"} {
		if !publicAddr(netip.MustParseAddr(addr)) {
			t.Errorf("%s counted as non-public", addr)
		}
	}
}

func TestImportSourceValidate(t *testing.T) {
	for _, ok := range []ImportSource{{Host: "ep-cool-name-123.us-east-2.aws.neon.tech", Port: 5432}, {Host: "203.0.113.9", Port: 1}, {Host: "2600:1f18::1", Port: 65535}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []ImportSource{{Host: "", Port: 5432}, {Host: "a b", Port: 5432}, {Host: "host:1", Port: 5432}, {Host: "db.example", Port: 0}, {Host: "db.example", Port: 70000}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// testCert issues a server certificate for host from a fresh CA.
func testCert(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "import test CA"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// fakeSource answers one connection like a PostgreSQL server: the
// SSLRequest gets answer, then (for 'S') TLS with cert, then it echoes.
func fakeSource(t *testing.T, answer byte, cert tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		request := make([]byte, 8)
		if _, err := io.ReadFull(conn, request); err != nil || !bytes.Equal(request, sslRequest) {
			return
		}
		if _, err := conn.Write([]byte{answer}); err != nil || answer != 'S' {
			return
		}
		server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		_, _ = io.Copy(server, server)
	}()
	return ln.Addr().String()
}

func TestImportRelayVerifiesTheSourcesTLS(t *testing.T) {
	cert, roots := testCert(t, "db.source.test")
	ctx := context.Background()
	dial := func(r *Runtime, addr, host string) (net.Conn, error) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		tlsConn, err := startPostgresTLS(ctx, conn, host, r.cfg.ImportRoots)
		if err != nil {
			_ = conn.Close()
		}
		return tlsConn, err
	}
	r := &Runtime{cfg: RuntimeConfig{ImportRoots: roots}}
	conn, err := dial(r, fakeSource(t, 'S', cert), "db.source.test")
	if err != nil {
		t.Fatalf("verified source refused: %v", err)
	}
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "hello" {
		t.Fatalf("echo %q, %v", got, err)
	}
	_ = conn.Close()
	if _, err := dial(r, fakeSource(t, 'S', cert), "other.source.test"); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("certificate for another host = %v, want refused", err)
	}
	if _, err := dial(r, fakeSource(t, 'N', cert), "db.source.test"); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("source without TLS = %v, want refused", err)
	}
	if _, err := dial(&Runtime{}, fakeSource(t, 'S', cert), "db.source.test"); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("source signed by an unknown CA = %v, want refused", err)
	}
}

func TestImportRelayRefusesPrivateSources(t *testing.T) {
	cert, roots := testCert(t, "localhost")
	addr := fakeSource(t, 'S', cert)
	port := netip.MustParseAddrPort(addr).Port()
	r := &Runtime{cfg: RuntimeConfig{ImportRoots: roots}}
	if _, err := r.dialImportSource(context.Background(), ImportSource{Host: "localhost", Port: int(port)}); !errors.Is(err, ErrImportSourceRefused) {
		t.Fatalf("loopback source = %v, want refused", err)
	}
	r.cfg.ImportAllowPrivate = true
	conn, err := r.dialImportSource(context.Background(), ImportSource{Host: "localhost", Port: int(port)})
	if err != nil {
		t.Fatalf("with private sources allowed: %v", err)
	}
	_ = conn.Close()
}
