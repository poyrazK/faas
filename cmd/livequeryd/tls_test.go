package main

import (
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
)

// adr: 20261016151947 — with -client-name the hub accepts realtimed's live
// client leaf and refuses every other leaf of the same fleet CA, so only
// realtimed can carry RLS claims.
func TestRealtimedTLSAcceptsOnlyTheNamedClient(t *testing.T) {
	root := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pki.EnsureFeatureLeaves(root, root, pki.FeatureLiveQueries, "single-box"); err != nil {
		t.Fatal(err)
	}
	var other pki.Role
	for _, role := range pki.Roles() {
		if role.Kind == pki.KindClient && role.Directory == "meterd" {
			other = role
			break
		}
	}
	if err := pki.EnsureLeaf(root, other, caCert, caKey, false); err != nil {
		t.Fatal(err)
	}
	caFile, _ := pki.CARoot(root)
	serverCert, serverKey := pki.LeafPaths(root, pki.FeatureRoles(pki.FeatureLiveQueries, "control-plane")[0])
	conf, err := realtimedTLS(serverCert, serverKey, caFile, pki.RealtimedLiveClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := realtimedTLS(serverCert, serverKey, "", pki.RealtimedLiveClient); err == nil {
		t.Error("-client-name without -client-ca was accepted")
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	handshake := func(clientRole pki.Role) error {
		ln, err := tls.Listen("tcp", "127.0.0.1:0", conf)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		serverErr := make(chan error, 1)
		go func() {
			c, err := ln.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			defer func() { _ = c.Close() }()
			serverErr <- c.(*tls.Conn).Handshake()
		}()
		certPath, keyPath := pki.LeafPaths(root, clientRole)
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		client, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
			RootCAs: pool, ServerName: pki.LivequerydServerName, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13,
		})
		if err == nil {
			_ = client.Close()
		}
		return <-serverErr
	}
	if err := handshake(pki.FeatureRoles(pki.FeatureLiveQueries, "compute-only")[0]); err != nil {
		t.Errorf("realtimed's live client leaf was refused: %v", err)
	}
	if err := handshake(other); err == nil {
		t.Error("another fleet client leaf was accepted")
	}
}
