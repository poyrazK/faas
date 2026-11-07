// adr: 20261103163954
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type edgeCertRows []edgecert.Stored

func (r edgeCertRows) Live(context.Context, string) ([]edgecert.Stored, error) { return r, nil }

// With the database source the listener serves the certificate edgednsd
// stored, its key opened in memory with the edge-tls identity; no
// certificate or key file is read.
func TestEdgeTLSListenerServesTheStoredCertificate(t *testing.T) {
	dir := t.TempDir()
	publicCA, zoneCA := newTestCA(t, "public"), newTestCA(t, "zone")
	leaf := publicCA.issue(t, x509.ExtKeyUsageServerAuth, "*.gregale.dev")
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(leaf.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{id.Recipient()}, edgecert.KeyNamespace, der, 0)
	if err != nil {
		t.Fatal(err)
	}
	row := edgecert.Stored{ID: 1, Names: []string{"*.gregale.dev"}, KeySealed: sealed, NotAfter: time.Now().Add(time.Hour),
		ChainPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}))}
	served, err := edgecert.NewServed(edgecert.CALetsEncrypt, []*age.X25519Identity{id}, edgeCertRows{row}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clientCAFile := filepath.Join(dir, "client-ca.pem")
	if err := os.WriteFile(clientCAFile, zoneCA.pem(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := edgeTLSConfig{addr: "127.0.0.1:0", clientCAFile: clientCAFile, front: edgeFrontNone, source: edgeCertFromDatabase, ca: edgecert.CALetsEncrypt, identityPath: "unused"}
	appHost := func(string) bool { return true }
	if _, err := openEdgeTLSListener(t.Context(), cfg, appHost, nil, edgeClientLimits{}, nil); err == nil {
		t.Fatal("a database source opened without its certificates")
	}
	if _, err := served.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ln, err := openEdgeTLSListener(t.Context(), cfg, appHost, served, edgeClientLimits{}, nil)
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
			go func() { _ = conn.(*tls.Conn).Handshake(); _ = conn.Close() }()
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(publicCA.cert)
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "app.gregale.dev"})
	if err != nil {
		t.Fatalf("handshake for an app host: %v", err)
	}
	if got := conn.ConnectionState().PeerCertificates[0]; !got.Equal(mustParse(t, leaf.Certificate[0])) {
		t.Fatalf("served %v, want the stored leaf", got.Subject)
	}
	_ = conn.Close()
	if conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "app.other.dev"}); err == nil {
		_ = conn.Close()
		t.Fatal("a name no stored certificate covers completed a handshake")
	}
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
