package main_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// fakePostgres answers an SSLRequest with 'S', completes TLS with a
// certificate for host, then echoes "world" for "hello".
func fakePostgres(t *testing.T, host string) (string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				req := make([]byte, 8)
				if _, err := io.ReadFull(conn, req); err != nil || binary.BigEndian.Uint32(req[4:]) != 80877103 {
					return
				}
				_, _ = conn.Write([]byte{'S'})
				tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
				msg := make([]byte, 5)
				if _, err := io.ReadFull(tc, msg); err != nil || string(msg) != "hello" {
					return
				}
				_, _ = tc.Write([]byte("world"))
			}()
		}
	}()
	return ln.Addr().String(), roots
}

func relayRoundTrip(t *testing.T, upstream, host, sslMode string, roots *x509.CertPool) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go main.PgRelayServeForTest(ctx, ln, upstream, host, sslMode, roots)
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	// A driver that tries TLS on the loopback leg is told no.
	ssl := make([]byte, 8)
	binary.BigEndian.PutUint32(ssl[:4], 8)
	binary.BigEndian.PutUint32(ssl[4:], 80877103)
	if _, err := conn.Write(ssl); err != nil {
		return "", err
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(conn, answer); err != nil || answer[0] != 'N' {
		t.Fatalf("loopback SSLRequest answer = %q %v, want N", answer, err)
	}
	// A real startup message is at least 8 bytes (length + protocol); the
	// fake server reads the first five.
	if _, err := conn.Write([]byte("hello-startup")); err != nil {
		return "", err
	}
	reply := make([]byte, 5)
	_, err = io.ReadFull(conn, reply)
	return string(reply), err
}

// adr: 20261107210000
func TestPgRelayVerifiesTheDatabaseHost(t *testing.T) {
	upstream, roots := fakePostgres(t, "db.example.test")
	if got, err := relayRoundTrip(t, upstream, "db.example.test", "verify-full", roots); err != nil || got != "world" {
		t.Fatalf("verify-full with the right host = %q %v, want world", got, err)
	}
	if got, err := relayRoundTrip(t, upstream, "other.example.test", "verify-full", roots); err == nil {
		t.Fatalf("verify-full with the wrong host relayed %q; it must refuse", got)
	}
	if got, err := relayRoundTrip(t, upstream, "other.example.test", "verify-ca", roots); err != nil || got != "world" {
		t.Fatalf("verify-ca ignores the host name: %q %v", got, err)
	}
	if got, err := relayRoundTrip(t, upstream, "db.example.test", "verify-full", x509.NewCertPool()); err == nil {
		t.Fatalf("an untrusted certificate relayed %q; it must refuse", got)
	}
	if got, err := relayRoundTrip(t, upstream, "anything", "require", nil); err != nil || got != "world" {
		t.Fatalf("require encrypts without verifying: %q %v", got, err)
	}
}

func TestLocalPostgresURL(t *testing.T) {
	got, err := main.LocalPostgresURLForTest("postgresql://app:p%40ss@db.example.test:6543/orders?sslmode=verify-full&sslrootcert=/x&options=-c%20search_path%3Dapp", 55432)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	q := u.Query()
	if u.Host != "127.0.0.1:55432" || u.User.Username() != "app" || pass != "p@ss" || u.Path != "/orders" ||
		q.Get("sslmode") != "disable" || q.Get("sslrootcert") != "" || q.Get("options") != "-c search_path=app" {
		t.Fatalf("local URL = %s", got)
	}
}

func TestDevLocalEnvLayering(t *testing.T) {
	env := main.DevLocalEnvForTest([]string{"DATABASE_URL=shell", "A=1"}, map[string]string{"DATABASE_URL": "remote"}, []string{"A=file"})
	last := map[string]string{}
	for _, kv := range env {
		k, v, _ := cutKV(kv)
		last[k] = v
	}
	if last["DATABASE_URL"] != "remote" || last["A"] != "file" {
		t.Fatalf("env = %v: remote resources must override the shell, and --env-file both", env)
	}
}

func cutKV(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}
