// adr: 1027 — vmmd on a database node survives SIGHUP, which the daily
// internal-PKI renewal sends, and serves the renewed leaf afterwards.
package main

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/pki"
)

func TestDatabaseServerTLSRotatesOnSIGHUP(t *testing.T) {
	issuer := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(issuer, false)
	if err != nil {
		t.Fatal(err)
	}
	san := pki.AltNames{IPAddresses: []net.IP{net.ParseIP("100.64.7.1")}}
	bundle := filepath.Join(t.TempDir(), "db-1")
	if err := pki.IssueTrustBundle(issuer, bundle, "database", "db-1.faas", san); err != nil {
		t.Fatal(err)
	}
	var server pki.Role
	for _, role := range pki.RolesForBox("database") {
		if role.Directory == "vmmd" && role.Filename == "server" {
			server = role
		}
	}
	certPath, keyPath := pki.LeafPaths(bundle, server)
	caPath, _ := pki.CARoot(bundle)
	cfg := &Config{TLSCertPath: certPath, TLSKeyPath: keyPath, TLSCAPath: caPath}

	tlsCfg, err := databaseServerTLS(t.Context(), cfg, nil, slog.New(slog.DiscardHandler))
	if err != nil || tlsCfg == nil || tlsCfg.GetConfigForClient == nil {
		t.Fatalf("server TLS = %v, %v", tlsCfg, err)
	}
	served := func() []byte {
		t.Helper()
		live, err := tlsCfg.GetConfigForClient(&tls.ClientHelloInfo{})
		if err != nil || len(live.Certificates) == 0 {
			t.Fatalf("live config = %v, %v", live, err)
		}
		return live.Certificates[0].Certificate[0]
	}
	before := served()
	if err := pki.EnsureLeafWithCNAndSANs(bundle, server, "db-1.faas", caCert, caKey, true, san); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for bytes.Equal(served(), before) {
		if time.Now().After(deadline) {
			t.Fatal("SIGHUP did not rotate the server leaf")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Without TLS there is nothing to rotate, and SIGHUP is still caught.
	none, err := databaseServerTLS(t.Context(), &Config{}, nil, slog.New(slog.DiscardHandler))
	if none != nil || err != nil {
		t.Fatalf("no TLS = %v, %v", none, err)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the process is still here
}
