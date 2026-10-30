package main

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/wire"
)

// registeredNodes is a NodeVerifier that knows a fixed set of compute nodes.
type registeredNodes map[string]bool

func (r registeredNodes) LookupCN(cn string) error {
	if !r[cn] {
		return errors.New("not a registered compute node")
	}
	return nil
}

// adr: 989 — the outbound-relay feature leaves work together: a compute
// node's vmmd, with the client leaf pkg/pki issued for its name, completes
// the relay handshake with outboundd's server leaf, and a node outboundd does
// not know is refused.
func TestRelayHandshakeWithFeatureLeaves(t *testing.T) {
	root := t.TempDir()
	if _, _, err := pki.EnsureCA(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.EnsureFeatureLeaves(root, root, pki.FeatureOutboundRelay, "control-plane"); err != nil {
		t.Fatal(err)
	}
	caPath, _ := pki.CARoot(root)
	serverCert, serverKey := pki.LeafPaths(root, pki.FeatureRoles(pki.FeatureOutboundRelay, "control-plane")[0])
	cfg := &Config{RelayTLSCertPath: serverCert, RelayTLSKeyPath: serverKey, RelayTLSCAPath: caPath}
	serverTLS, err := cfg.relayTLSConfig(registeredNodes{"fsn-2.faas": true})
	if err != nil {
		t.Fatal(err)
	}

	handshake := func(node string) error {
		t.Helper()
		nodeRoot := t.TempDir()
		if _, err := pki.EnsureFeatureLeavesForNode(root, nodeRoot, pki.FeatureOutboundRelay, "compute-only", node); err != nil {
			t.Fatal(err)
		}
		clientCert, clientKey := pki.LeafPaths(nodeRoot, pki.FeatureRoles(pki.FeatureOutboundRelay, "compute-only")[0])
		clientTLS, err := wire.LoadClientTLSConfigWithPrefix("vmmd_outbound_client_", clientCert, clientKey, caPath)
		if err != nil {
			t.Fatal(err)
		}
		clientTLS.ServerName = pki.OutbounddRelayServerName // what vmmd dials as
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		done := make(chan error, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				done <- err
				return
			}
			defer func() { _ = conn.Close() }()
			done <- tls.Server(conn, serverTLS).Handshake()
		}()
		// With TLS 1.3 the client finishes before the server checks its
		// certificate, so the server's result is the one that counts.
		if conn, err := tls.Dial("tcp", ln.Addr().String(), clientTLS); err == nil {
			defer func() { _ = conn.Close() }()
		}
		return <-done
	}
	if err := handshake("fsn-2.faas"); err != nil {
		t.Fatalf("registered node handshake: %v", err)
	}
	if err := handshake("fsn-9.faas"); err == nil {
		t.Fatal("an unregistered node completed the relay handshake")
	}
}
